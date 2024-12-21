// internal/bots/bot.go
package bots

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type UserSettings struct {
	ChangeThreshold float64 `json:"change_threshold"`
	TimeFrame       string  `json:"time_frame"`
	TargetBot       string  `json:"target_bot"`
	MonitorOI       bool    `json:"monitor_oi"`
	OIThreshold     float64 `json:"oi_threshold"`
}

type OnSettingsChangeFunc func(userID int64, s UserSettings)

type userSession struct {
	MessageID int
	ChatID    int64
	States    []string
}

type Bot struct {
	BotAPI       *tgbotapi.BotAPI
	Users        map[int64]*UserSettings
	Mu           sync.Mutex
	OnSettingsFn OnSettingsChangeFunc
	ManagerRef   *BotManager
	UserSessions map[int64]*userSession
}

func NewBot(token string) (*Bot, error) {
	botAPI, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}
	return &Bot{
		BotAPI:       botAPI,
		Users:        make(map[int64]*UserSettings),
		UserSessions: make(map[int64]*userSession),
	}, nil
}

func (b *Bot) HandleUpdate(update tgbotapi.Update) {
	if update.Message.IsCommand() && update.Message.Command() == "start" {
		if args := strings.TrimSpace(update.Message.CommandArguments()); args != "" {
			log.Printf("User %d passed parameters: %s", update.Message.Chat.ID, args)
			// Парсинг args и настройка пользователя
		}
	}
	if update.Message != nil && update.Message.IsCommand() {
		switch strings.ToLower(update.Message.Command()) {
		case "start":
			b.startCommand(update.Message.Chat.ID, update.Message.From.FirstName)
		case "help":
			b.sendHelp(update.Message.Chat.ID)
		default:
			b.sendUnknown(update.Message.Chat.ID)
		}
	} else if update.Message != nil {
		b.sendUnknown(update.Message.Chat.ID)
	} else if update.CallbackQuery != nil {
		b.handleCallbackQuery(update.CallbackQuery)
	}
}

func (b *Bot) startCommand(chatID int64, firstName string) {
	msgText := fmt.Sprintf("🚀 Привет, %s! Добро пожаловать в наш бот. Выберите действие:", firstName)
	btn := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📄 Описание функционала", "to:description"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚠️ Отказ от ответственности", "to:disclaimer"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚀 Поехали", "to:choose_main_mode"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, msgText)
	msg.ReplyMarkup = btn
	sentMsg, err := b.BotAPI.Send(msg)
	if err != nil {
		log.Printf("Error sending start message to %d: %v", chatID, err)
		return
	}

	b.UserSessions[chatID] = &userSession{
		ChatID:    chatID,
		MessageID: sentMsg.MessageID,
		States:    []string{"welcome"},
	}
	log.Printf("User %d started the setup", chatID)
}

func (b *Bot) sendHelp(chatID int64) {
	helpText := "📖 *Справка по командам бота:*\n\n" +
		"/start - Начать работу с ботом\n" +
		"/help - Показать это сообщение со справкой\n\n" +
		"Этот бот отслеживает изменения цен и открытого интереса (OI) на фьючерсы криптовалют на Binance.\n" +
		"Доступны режимы Scalp, Intraday и Spot."
	msg := tgbotapi.NewMessage(chatID, helpText)
	msg.ParseMode = "Markdown"
	if _, err := b.BotAPI.Send(msg); err != nil {
		log.Printf("Error sending help to %d: %v", chatID, err)
	}
}

func (b *Bot) sendUnknown(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "❓ Неизвестная команда. Используйте /start или /help.")
	if _, err := b.BotAPI.Send(msg); err != nil {
		log.Printf("Error sending unknown command message to %d: %v", chatID, err)
	}
}

func (b *Bot) handleCallbackQuery(callback *tgbotapi.CallbackQuery) {
	chatID := callback.Message.Chat.ID
	data := callback.Data

	sess, ok := b.UserSessions[chatID]
	if !ok {
		b.BotAPI.Request(tgbotapi.NewCallback(callback.ID, ""))
		return
	}

	b.Mu.Lock()
	defer b.Mu.Unlock()

	log.Printf("User %d pressed button: %s", chatID, data)

	switch {
	case data == "back":
		b.popState(chatID)
		b.renderState(chatID)
	case data == "back_to_start":
		b.UserSessions[chatID].States = []string{"welcome"}
		b.Users[chatID] = &UserSettings{}
		b.renderState(chatID)
	case data == "to:description":
		b.showDescription(chatID)
	case data == "to:disclaimer":
		b.showDisclaimer(chatID)
	case data == "to:choose_main_mode":
		b.pushState(chatID, "choose_main_mode")
		b.renderState(chatID)
	case strings.HasPrefix(data, "to:"):
		newState := strings.TrimPrefix(data, "to:")
		b.pushState(chatID, newState)
		b.renderState(chatID)
	case strings.HasPrefix(data, "set_change:"):
		v := strings.TrimPrefix(data, "set_change:")
		th, err := strconv.ParseFloat(v, 64)
		if err != nil {
			b.editError(chatID, sess)
			b.BotAPI.Request(tgbotapi.NewCallback(callback.ID, ""))
			return
		}
		if _, exists := b.Users[chatID]; !exists {
			b.Users[chatID] = &UserSettings{}
		}
		b.Users[chatID].ChangeThreshold = th
		currentState := b.currentState(chatID)
		if currentState == "pumps_dumps" {
			b.pushState(chatID, "choose_timeframe")
		}
		b.renderState(chatID)
	case strings.HasPrefix(data, "set_time:"):
		tf := strings.TrimPrefix(data, "set_time:")
		if _, exists := b.Users[chatID]; !exists {
			b.Users[chatID] = &UserSettings{}
		}
		b.Users[chatID].TimeFrame = tf
		b.pushState(chatID, "choose_target_bot")
		b.renderState(chatID)
	case strings.HasPrefix(data, "target:"):
		bn := strings.TrimPrefix(data, "target:")
		if _, exists := b.Users[chatID]; !exists {
			b.Users[chatID] = &UserSettings{}
		}
		b.Users[chatID].TargetBot = bn
		if b.OnSettingsFn != nil {
			b.OnSettingsFn(chatID, *b.Users[chatID])
		}
		b.pushState(chatID, "final")
		b.renderState(chatID)
	case strings.HasPrefix(data, "set_oi_threshold:"):
		v := strings.TrimPrefix(data, "set_oi_threshold:")
		oiTh, err := strconv.ParseFloat(v, 64)
		if err != nil {
			b.editError(chatID, sess)
			b.BotAPI.Request(tgbotapi.NewCallback(callback.ID, ""))
			return
		}
		if _, exists := b.Users[chatID]; !exists {
			b.Users[chatID] = &UserSettings{}
		}
		b.Users[chatID].MonitorOI = true
		b.Users[chatID].OIThreshold = oiTh
		b.pushState(chatID, "choose_target_bot")
		b.renderState(chatID)
	case strings.HasPrefix(data, "set_pd:"):
		parts := strings.Split(strings.TrimPrefix(data, "set_pd:"), ":")
		if len(parts) != 2 {
			b.editError(chatID, sess)
			b.BotAPI.Request(tgbotapi.NewCallback(callback.ID, ""))
			return
		}
		pdPercent, err1 := strconv.ParseFloat(parts[0], 64)
		pdMinutes, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			b.editError(chatID, sess)
			b.BotAPI.Request(tgbotapi.NewCallback(callback.ID, ""))
			return
		}
		if _, exists := b.Users[chatID]; !exists {
			b.Users[chatID] = &UserSettings{}
		}
		b.Users[chatID].ChangeThreshold = pdPercent
		b.Users[chatID].TimeFrame = fmt.Sprintf("%dm", pdMinutes)
		b.pushState(chatID, "choose_target_bot")
		b.renderState(chatID)
	default:
		log.Printf("Unknown callback data from user %d: %s", chatID, data)
		b.sendUnknown(chatID)
	}

	b.BotAPI.Request(tgbotapi.NewCallback(callback.ID, ""))
}

func (b *Bot) pushState(chatID int64, st string) {
	sess := b.UserSessions[chatID]
	sess.States = append(sess.States, st)
}

func (b *Bot) popState(chatID int64) {
	sess := b.UserSessions[chatID]
	if len(sess.States) > 1 {
		sess.States = sess.States[:len(sess.States)-1]
	}
}

func (b *Bot) currentState(chatID int64) string {
	sess := b.UserSessions[chatID]
	return sess.States[len(sess.States)-1]
}

func (b *Bot) renderState(chatID int64) {
	sess := b.UserSessions[chatID]
	state := b.currentState(chatID)

	var text string
	var btn tgbotapi.InlineKeyboardMarkup

	switch state {
	case "welcome":
		text = "🚀 Привет! Добро пожаловать в наш бот. Выберите действие:"
		btn = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📄 Описание функционала", "to:description"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⚠️ Отказ от ответственности", "to:disclaimer"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🚀 Поехали", "to:choose_main_mode"),
			),
		)
	case "description":
		text = "📄 *Описание функционала:*\n\n[Ваше описание функционала будет здесь](https://t.me/your_telegraph_link)"
		btn = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "back"),
			),
		)
	case "disclaimer":
		text = "⚠️ *Отказ от ответственности:*\n\n[Ваш текст отказа от ответственности будет здесь]"
		btn = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "back"),
			),
		)
	case "choose_main_mode":
		text = "📂 Выберите режим:"
		btn = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⚡ Scalp Mode", "to:scalp_mode"),
				tgbotapi.NewInlineKeyboardButtonData("⏱ Intraday", "to:intraday_mode"),
				tgbotapi.NewInlineKeyboardButtonData("💰 Spot Mode", "to:spot_mode"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "back"),
			),
		)
	case "scalp_mode":
		text = "🔧 Scalp Mode выбран! Выберите метрику:"
		btn = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📈 Pumps/Dumps", "to:pumps_dumps"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "back"),
			),
		)
	case "pumps_dumps":
		text = "📉 Порог изменения цены (%):"
		btn = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ 2%", "set_change:2"),
				tgbotapi.NewInlineKeyboardButtonData("✅ 2.5%", "set_change:2.5"),
				tgbotapi.NewInlineKeyboardButtonData("✅ 3%", "set_change:3"),
				tgbotapi.NewInlineKeyboardButtonData("✅ 5%", "set_change:5"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "back"),
			),
		)
	case "choose_timeframe":
		text = "⏱ Выберите интервал:"
		btn = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⏱ 1m", "set_time:1m"),
				tgbotapi.NewInlineKeyboardButtonData("⏱ 3m", "set_time:3m"),
				tgbotapi.NewInlineKeyboardButtonData("⏱ 5m", "set_time:5m"),
				tgbotapi.NewInlineKeyboardButtonData("⏱ 15m", "set_time:15m"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "back"),
			),
		)
	case "intraday_mode":
		text = "⏱ Intraday Mode выбран! Выберите метрику:"
		btn = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📊 Изменение OI", "to:intraday_oi"),
				tgbotapi.NewInlineKeyboardButtonData("📈 Pumps/Dumps", "to:intraday_pumps_dumps"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "back"),
			),
		)
	case "intraday_oi":
		text = "📊 Порог изменения OI (%):"
		btn = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ 2.5%", "set_oi_threshold:2.5"),
				tgbotapi.NewInlineKeyboardButtonData("✅ 5%", "set_oi_threshold:5"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "back"),
			),
		)
	case "intraday_pumps_dumps":
		text = "📈 Выберите параметр Pumps/Dumps:"
		btn = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ 5% / 15m", "set_pd:5:15"),
				tgbotapi.NewInlineKeyboardButtonData("✅ 10% / 30m", "set_pd:10:30"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "back"),
			),
		)
	case "choose_target_bot":
		text = "🤖 Выберите бота для уведомлений:"
		btn = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("💹 Bot1", "target:bot1"),
				tgbotapi.NewInlineKeyboardButtonData("💹 Bot2", "target:bot2"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("💹 Bot3", "target:bot3"),
				tgbotapi.NewInlineKeyboardButtonData("💹 Bot4", "target:bot4"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Вернуться в начало", "choose_main_mode"),
			),
		)
	case "final":
		s := b.Users[chatID]
		botUsername, ok := b.ManagerRef.Usernames[s.TargetBot]
		if !ok || botUsername == "" {
			botUsername = "gmscreener1_bot" // Default username, измените при необходимости
		}
		link := fmt.Sprintf("https://t.me/%s", botUsername)
		text = fmt.Sprintf("✅ Мониторинг запущен!\nНажмите кнопку, чтобы перейти в бота: [t.me/%s](%s)", botUsername, link)
		btn = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonURL("👉 Перейти в бота", link),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Вернуться в начало", "back_to_start"),
			),
		)
	default:
		text = "❓ Неизвестное состояние. Используйте /start."
		btn = tgbotapi.NewInlineKeyboardMarkup()
	}

	edit := tgbotapi.NewEditMessageTextAndMarkup(sess.ChatID, sess.MessageID, text, btn)
	edit.ParseMode = "Markdown"
	if _, err := b.BotAPI.Send(edit); err != nil {
		log.Printf("Error editing message for user %d: %v", chatID, err)
	}
}

func (b *Bot) showDescription(chatID int64) {
	text := "📄 *Описание функционала:*\n\n[Ваше описание функционала будет здесь](https://t.me/your_telegraph_link)"
	btn := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "back"),
		),
	)
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, b.UserSessions[chatID].MessageID, text, btn)
	edit.ParseMode = "Markdown"
	if _, err := b.BotAPI.Send(edit); err != nil {
		log.Printf("Error editing description message for user %d: %v", chatID, err)
	}
}

func (b *Bot) showDisclaimer(chatID int64) {
	text := "⚠️ *Отказ от ответственности:*\n\n[Ваш текст отказа от ответственности будет здесь]"
	btn := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "back"),
		),
	)
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, b.UserSessions[chatID].MessageID, text, btn)
	edit.ParseMode = "Markdown"
	if _, err := b.BotAPI.Send(edit); err != nil {
		log.Printf("Error editing disclaimer message for user %d: %v", chatID, err)
	}
}

func (b *Bot) editError(chatID int64, sess *userSession) {
	text := "❌ Произошла ошибка при обработке вашего запроса. Попробуйте снова."
	btn := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Назад", "back"),
		),
	)
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, sess.MessageID, text, btn)
	edit.ParseMode = "Markdown"
	if _, err := b.BotAPI.Send(edit); err != nil {
		log.Printf("Error editing error message for user %d: %v", chatID, err)
	}
}
