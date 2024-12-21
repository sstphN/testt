// persistence/monitor.go
package persistence

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"1233/internal/bots"
	"1233/internal/exchanges/binance"

	"github.com/adshao/go-binance/v2/futures"
)

type OIRecord struct {
	Timestamp time.Time
	OI        float64
}

type UserOITracking struct {
	Symbols map[string]*SymbolOITracking
	Mu      sync.Mutex
}

type SymbolOITracking struct {
	Records       []OIRecord
	LastAlertTime time.Time // Для отслеживания времени последнего алерта
}

var userOITrackings = make(map[int64]*UserOITracking)
var uOTMu sync.Mutex

const alertCooldown = 5 * time.Minute // Минимальный интервал между алертами

// Проверка значительных изменений
func hasSignificantChange(current, previous, threshold float64) bool {
	if previous == 0 {
		return false // Избегаем деления на ноль
	}
	change := math.Abs((current - previous) / previous * 100)
	return change >= threshold
}

// Метод для проверки возможности отправки алерта
func (s *SymbolOITracking) shouldSendAlert() bool {
	if time.Since(s.LastAlertTime) > alertCooldown {
		s.LastAlertTime = time.Now()
		return true
	}
	return false
}

func StartMonitoring(ctx context.Context, userID int64, s bots.UserSettings, symbols []string, sendFunc func(int64, string), c *futures.Client) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	uOTMu.Lock()
	if _, exists := userOITrackings[userID]; !exists {
		userOITrackings[userID] = &UserOITracking{
			Symbols: make(map[string]*SymbolOITracking),
		}
		for _, sym := range symbols {
			currentOI, err := binance.GetOpenInterest(c, sym)
			if err != nil {
				log.Printf("[User %d] Ошибка инициализации OI для %s: %v", userID, sym, err)
				continue
			}
			userOITrackings[userID].Symbols[sym] = &SymbolOITracking{
				Records: []OIRecord{
					{
						Timestamp: time.Now(),
						OI:        currentOI,
					},
				},
				LastAlertTime: time.Time{}, // Инициализируем нулевым значением
			}
			log.Printf("[User %d] Инициализировано начальное OI для %s: %.2f", userID, sym, currentOI)
		}
		log.Printf("[User %d] Старт мониторинга OI для символов: %v", userID, symbols)
	}
	uOTMu.Unlock()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[User %d] Завершение мониторинга", userID)
			return
		case <-ticker.C:
			// Проверка изменения цены (только для Scalp Mode)
			if s.TimeFrame != "" && s.ChangeThreshold > 0 {
				for _, sym := range symbols {
					prevClose, currClose, err := binance.GetChangePercent(c, ctx, sym, s.TimeFrame)
					if err != nil {
						log.Printf("[User %d] Ошибка получения изменения цены для %s: %v", userID, sym, err)
						continue
					}
					cp := ((currClose - prevClose) / prevClose) * 100
					if math.Abs(cp) >= s.ChangeThreshold {
						d := "🟥 Dump"
						if cp > 0 {
							d = "🟩 Pump"
						}
						msg := fmt.Sprintf("%s: %s\nИзменение цены: %.2f%%\nТекущая цена: %.4f USDT", d, sym, cp, currClose)
						log.Printf("[User %d] Срабатывание по цене для %s: %.2f%%", userID, sym, cp)
						sendFunc(userID, msg)
					}
				}
			}

			// Проверка изменения OI
			if s.MonitorOI && s.OIThreshold > 0 {
				uOTMu.Lock()
				tracking, exists := userOITrackings[userID]
				if !exists {
					tracking = &UserOITracking{
						Symbols: make(map[string]*SymbolOITracking),
					}
					for _, sym := range symbols {
						tracking.Symbols[sym] = &SymbolOITracking{
							Records:       []OIRecord{},
							LastAlertTime: time.Time{},
						}
					}
					userOITrackings[userID] = tracking
					log.Printf("[User %d] Инициализация трекинга OI для символов: %v", userID, symbols)
				}
				uOTMu.Unlock()

				for _, sym := range symbols {
					currentOI, err := binance.GetOpenInterest(c, sym)
					if err != nil {
						log.Printf("[User %d] Ошибка получения OI для %s: %v", userID, sym, err)
						continue
					}

					tracking.Mu.Lock()
					sTracking, ok := tracking.Symbols[sym]
					if !ok {
						sTracking = &SymbolOITracking{
							Records:       []OIRecord{},
							LastAlertTime: time.Time{},
						}
						tracking.Symbols[sym] = sTracking
						log.Printf("[User %d] Добавлен трекинг OI для нового символа: %s", userID, sym)
					}

					now := time.Now()
					sTracking.Records = append(sTracking.Records, OIRecord{
						Timestamp: now,
						OI:        currentOI,
					})

					// Очищаем старые записи
					cutoff := now.Add(-30 * time.Minute)
					var filtered []OIRecord
					for _, rec := range sTracking.Records {
						if rec.Timestamp.After(cutoff) {
							filtered = append(filtered, rec)
						}
					}
					sTracking.Records = filtered

					var oi15m, oi30m float64
					for _, rec := range sTracking.Records {
						// Используем более гибкий поиск, допускающий небольшое отклонение во времени
						if rec.Timestamp.Before(now.Add(-15*time.Minute)) && rec.Timestamp.After(now.Add(-16*time.Minute)) {
							oi15m = rec.OI
						}
						if rec.Timestamp.Before(now.Add(-30*time.Minute)) && rec.Timestamp.After(now.Add(-31*time.Minute)) {
							oi30m = rec.OI
						}
					}
					tracking.Mu.Unlock()

					// Если нет данных за 15 или 30 мин, пропускаем
					if oi15m == 0 && oi30m == 0 {
						continue
					}

					var shouldAlert bool
					var msg string

					if oi15m != 0 && hasSignificantChange(currentOI, oi15m, s.OIThreshold) {
						if sTracking.shouldSendAlert() {
							change15m := ((currentOI - oi15m) / oi15m) * 100
							msg += fmt.Sprintf("OI Change (15m): %.2f%%\n", change15m)
							shouldAlert = true
						}
					}

					if oi30m != 0 && hasSignificantChange(currentOI, oi30m, s.OIThreshold) {
						if sTracking.shouldSendAlert() {
							change30m := ((currentOI - oi30m) / oi30m) * 100
							msg += fmt.Sprintf("OI Change (30m): %.2f%%\n", change30m)
							shouldAlert = true
						}
					}

					if shouldAlert {
						price, err := binance.GetCurrentPrice(c, sym)
						if err != nil {
							log.Printf("[User %d] Ошибка получения текущей цены для %s: %v", userID, sym, err)
							continue
						}
						finalMsg := fmt.Sprintf("🎰 OI Alert\n`%s` Binance\n%sТекущая цена: %.5f USDT", sym, msg, price)
						log.Printf("[User %d] Срабатывание по OI для %s", userID, sym)
						sendFunc(userID, finalMsg)
					}
				}
			}
		}
	}
}
