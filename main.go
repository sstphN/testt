package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"1333/internal/bots"
	"1333/internal/exchanges/binance"
	"1333/persistence"

	"github.com/jackc/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

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
	connectToDB()
	defer dbPool.Close()

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

func addUserCommand(userID int64, args []string) string {

	timeFrame := args[0]
	targetBot := args[1]
	preferredExchanges := args[2:]
	oiThreshold := 5.0
	monitorOI := true
	mode := "intraday"
	changeThreshold := 1.0

	err := insertUserData(userID, timeFrame, targetBot, preferredExchanges, float32(oiThreshold), monitorOI, mode, float32(changeThreshold))
	if err != nil {
		return "Ошибка сохранения данных: " + err.Error()
	}
	return "Ваши данные успешно сохранены!"
}

var dbPool *pgxpool.Pool

func connectToDB() {
	dsn := "postgres://postgrese:221224@localhost:5432/gms_bot_db"
	var err error
	dbPool, err = pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatalf("Ошибка подключения к базе данных: %v\n", err)
	}
	log.Println("Connected to the database!")
}

func insertUserData(userID int64, timeFrame, targetBot string, preferredExchanges []string, oiThreshold float32, monitorOI bool, mode string, changeThreshold float32) error {
	exchangesArray := &pgtype.TextArray{}
	if err := exchangesArray.Set(preferredExchanges); err != nil {
		return err
	}

	query := `INSERT INTO your_table_name
(user_id, time_frame, target_bot, preferred_exchanges, oi_threshold, monitor_oi, mode, change_threshold)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := dbPool.Exec(context.Background(), query, userID, timeFrame, targetBot, exchangesArray, oiThreshold, monitorOI, mode, changeThreshold)
	return err
}

func getUserData(userID int64) (map[string]interface{}, error) {
	query := `
SELECT user_id, time_frame, target_bot, preferred_exchanges, oi_threshold, monitor_oi, mode, change_threshold
FROM your_table_name WHERE user_id = $1`

	row := dbPool.QueryRow(context.Background(), query, userID)

	var timeFrame, targetBot, mode string
	var preferredExchanges []string
	var oiThreshold, changeThreshold float32
	var monitorOI bool

	err := row.Scan(&userID, &timeFrame, &targetBot, &preferredExchanges,
		&oiThreshold, &monitorOI, &mode, &changeThreshold)

	if err != nil {
		return nil, err
	}

	result := map[string]interface{}{
		"user_id":             userID,
		"time_frame":          timeFrame,
		"target_bot":          targetBot,
		"preferred_exchanges": preferredExchanges,
		"oi_threshold":        oiThreshold,
		"monitor_oi":          monitorOI,
		"mode":                mode,
		"change_threshold":    changeThreshold,
	}
	return result, nil
}
