package binance

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

func NewClient(apiKey, apiSecret string) *futures.Client {
	client := futures.NewClient(apiKey, apiSecret)
	return client
}

func GetUSDMFuturesSymbols(client *futures.Client, ctx context.Context) ([]string, error) {
	exchangeInfo, err := client.NewExchangeInfoService().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get exchange info: %w", err)
	}

	var symbols []string
	for _, s := range exchangeInfo.Symbols {
		if s.ContractType == "PERPETUAL" && s.QuoteAsset == "USDT" && s.Status == "TRADING" {
			symbols = append(symbols, s.Symbol)
		}
	}
	return symbols, nil
}

func GetChangePercent(client *futures.Client, ctx context.Context, symbol, timeframe string) (prevClose float64, currClose float64, err error) {
	klines, err := client.NewKlinesService().
		Symbol(symbol).
		Interval(timeframe).
		Limit(2).
		Do(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get klines: %w", err)
	}
	if len(klines) < 2 {
		return 0, 0, fmt.Errorf("not enough klines received")
	}

	prevClose, err = strconv.ParseFloat(klines[0].Close, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse previous close: %w", err)
	}

	currClose, err = strconv.ParseFloat(klines[1].Close, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse current close: %w", err)
	}

	return prevClose, currClose, nil
}

func GetOpenInterest(client *futures.Client, symbol string) (float64, error) {
	startTime := time.Now()
	defer func() {
		log.Printf("[DEBUG] GetOpenInterest for %s took %s", symbol, time.Since(startTime))
	}()
	oi, err := client.NewGetOpenInterestService().Symbol(symbol).Do(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get open interest for %s: %w", symbol, err)
	}

	oiValue, err := strconv.ParseFloat(oi.OpenInterest, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse open interest value for %s: %w", symbol, err)
	}

	return oiValue, nil
}

func GetCurrentPrice(client *futures.Client, symbol string) (float64, error) {
	prices, err := client.NewListPricesService().
		Symbol(symbol).
		Do(context.Background())
	if err != nil || len(prices) == 0 {
		return 0, fmt.Errorf("failed to get price for %s: %w", symbol, err)
	}
	return strconv.ParseFloat(prices[0].Price, 64)
}

func InitializeOI(client *futures.Client, symbols []string) map[string]float64 {
	results := make(map[string]float64)
	mu := sync.Mutex{}
	wg := sync.WaitGroup{}

	for _, symbol := range symbols {
		wg.Add(1)
		go func(sym string) {
			defer wg.Done()
			oi, err := GetOpenInterest(client, sym)
			if err != nil {
				log.Printf("[ERROR] Failed to get OI for %s: %v", sym, err)
				return
			}
			mu.Lock()
			results[sym] = oi
			mu.Unlock()

		}(symbol)
	}

	wg.Wait()
	return results
}
