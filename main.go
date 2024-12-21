// main.go
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"1233/internal/bots"
	"1233/internal/exchanges/binance"
	"1233/persistence"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Config struct {
	MainBotToken        string            `json:"main_bot_token"`
	AdditionalBots      map[string]string `json:"additional_bots"`
	AdditionalUsernames map[string]string `json:"additional_usernames"`
	BinanceAPIKey       string            `json:"binance_api_key"`
	BinanceAPISecret    string            `json:"binance_api_secret"`
}

func main() {
	cfg, err := loadConfig("configs/config.json")
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr, err := bots.NewBotManager(cfg.MainBotToken, cfg.AdditionalBots, cfg.AdditionalUsernames)
	if err != nil {
		log.Fatal(err)
	}

	store := persistence.NewUserStore("data/user_store.json")
	if err := store.Load(); err != nil {
		log.Printf("load error: %v", err)
	}
	mgr.Bots["main"].Users = store.All()
	mgr.Bots["main"].OnSettingsFn = func(userID int64, s bots.UserSettings) {
		store.Set(userID, s)
		if err := store.Save(); err != nil {
			log.Printf("Error saving user settings for %d: %v", userID, err)
		}
		go startUserMonitoring(ctx, userID, &s, cfg.BinanceAPIKey, cfg.BinanceAPISecret, mgr)
	}

	for uid, us := range mgr.Bots["main"].Users {
		if (us.TimeFrame != "" && us.ChangeThreshold > 0 && us.TargetBot != "") || (us.MonitorOI && us.OIThreshold > 0) {
			go startUserMonitoring(ctx, uid, us, cfg.BinanceAPIKey, cfg.BinanceAPISecret, mgr)
		}
	}

	go func() {
		u := tgbotapi.NewUpdate(0)
		updates := mgr.Bots["main"].BotAPI.GetUpdatesChan(u)
		for update := range updates {
			mgr.Bots["main"].HandleUpdate(update)
		}
	}()

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, os.Interrupt, syscall.SIGTERM)
	<-sigC
}

func startUserMonitoring(ctx context.Context, userID int64, s *bots.UserSettings, apiKey, apiSecret string, mgr *bots.BotManager) {
	c := binance.NewClient(apiKey, apiSecret)
	symbols, err := binance.GetUSDMFuturesSymbols(c, ctx)
	if err != nil || len(symbols) == 0 {
		log.Printf("Пользователь %d: нет доступных символов для мониторинга", userID)
		return
	}
	persistence.StartMonitoring(ctx, userID, *s, symbols, func(u int64, text string) {
		mgr.SendToBot(s.TargetBot, u, text)
	}, c)
}

func loadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var c Config
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		return nil, err
	}
	return &c, nil
}
