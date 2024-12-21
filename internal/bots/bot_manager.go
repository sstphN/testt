// internal/bots/bot_manager.go
package bots

import (
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type BotManager struct {
	Bots      map[string]*Bot
	Usernames map[string]string
}

func NewBotManager(mainToken string, additional map[string]string, usernames map[string]string) (*BotManager, error) {
	manager := &BotManager{
		Bots:      make(map[string]*Bot),
		Usernames: usernames,
	}
	mainBot, err := NewBot(mainToken)
	if err != nil {
		return nil, err
	}
	manager.Bots["main"] = mainBot
	mainBot.ManagerRef = manager

	for name, token := range additional {
		bot, err := NewBot(token)
		if err != nil {
			log.Printf("Ошибка инициализации дополнительного бота %s: %v", name, err)
			continue
		}
		manager.Bots[name] = bot
	}
	return manager, nil
}

func (m *BotManager) SendToBot(botName string, chatID int64, text string) {
	b, ok := m.Bots[botName]
	if !ok {
		log.Printf("Бот с именем %s не найден.", botName)
		return
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	_, err := b.BotAPI.Send(msg) // Мы игнорируем первый возвращаемый объект tgbotapi.Message
	if err != nil {
		log.Printf("Ошибка при отправке сообщения через бота %s: %v", botName, err)
	} else {
		log.Printf("Отправлено сообщение пользователю %d через бота %s: %s", chatID, botName, text)
	}
}
