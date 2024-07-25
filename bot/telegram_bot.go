package bot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"fmt"

	"github.com/sirupsen/logrus"
)

// TelegramBot 是封装的 Telegram 机器人接口结构体
type TelegramBot struct {
	BotToken string // 机器人访问令牌
	ChatID   int64  // 聊天 ID，表示消息发送的目标聊天
}

// NewTelegramBot 创建一个新的 TelegramBot 实例
// botToken 参数是机器人的访问令牌，chatID 参数是消息发送的目标聊天 ID
func NewTelegramBot(botToken string, chatID int64) *TelegramBot {
	return &TelegramBot{
		BotToken: botToken,
		ChatID:   chatID,
	}
}

// SendMessage 发送消息到 Telegram
// message 参数是要发送的消息内容，parseMode 参数指定消息解析模式（例如 "Markdown" 或 "HTML"）
func (bot *TelegramBot) SendMessage(message, parseMode string) error {
	// 构建发送消息的 API URL
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", bot.BotToken)

	// 创建一个包含消息内容的字典
	data := map[string]interface{}{
		"chat_id":    bot.ChatID,    // 目标聊天 ID
		"text":       message,       // 消息内容
		"parse_mode": parseMode,     // 消息解析模式
	}

	// 将字典编码为 JSON 格式
	body, err := json.Marshal(data)
	if err != nil {
		// 如果 JSON 编码失败，使用 logrus 记录错误并返回错误
		logrus.Errorf("Failed to marshal JSON: %v", err)
		return err
	}

	// 使用 http.Post 方法发送 POST 请求，内容类型为 "application/json"
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		// 如果发送请求失败，使用 logrus 记录错误并返回错误
		logrus.Errorf("Failed to send message: %v", err)
		return err
	}
	defer resp.Body.Close() // 确保响应体在函数退出时关闭

	// 检查响应状态码是否为 200 OK
	if resp.StatusCode != http.StatusOK {
		// 如果状态码不是 200，使用 logrus 记录错误并返回错误
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		logrus.Error(err)
		return err
	}

	// 使用 logrus 记录成功发送消息的信息
	logrus.Infof("Message sent successfully: %s", message)

	// 如果没有错误，返回 nil
	return nil
}
