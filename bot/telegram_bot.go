package bot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"fmt"

	"github.com/sirupsen/logrus"
)

type TelegramBot struct {
	Token   string
	ChatIDs []int64
}

func NewTelegramBot(token string, chatIDs []int64) *TelegramBot {
	return &TelegramBot{
		Token:   token,
		ChatIDs: chatIDs,
	}
}

func (bot *TelegramBot) SendMessage(message, parseMode string) error {
	for _, chatID := range bot.ChatIDs {
		err := bot.sendToChatID(chatID, message, parseMode)
		if err != nil {
			logrus.Errorf("Failed to send message to chat ID %d: %v", chatID, err)
			return err
		}
	}
	return nil
}

func (bot *TelegramBot) sendToChatID(chatID int64, message, parseMode string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", bot.Token)
	data := map[string]interface{}{
		"chat_id":    chatID,
		"text":       message,
		"parse_mode": parseMode,
	}

	body, err := json.Marshal(data)
	if err != nil {
		logrus.Errorf("Failed to marshal JSON: %v", err)
		return err
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		logrus.Errorf("Failed to send message: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		logrus.Error(err)
		return err
	}

	logrus.Infof("Message sent successfully to chat ID %d", chatID)
	return nil
}
