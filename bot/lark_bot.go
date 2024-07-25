package bot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"fmt"

	"github.com/sirupsen/logrus"
)

// LarkBot 是一个结构体，包含一个 WebhookURL 字段，用于存储飞书机器人的 Webhook URL。
type LarkBot struct {
	WebhookURL string
}

// NewLarkBot 是一个构造函数，接受一个 webhookURL 参数，并返回一个 LarkBot 指针。
func NewLarkBot(webhookURL string) *LarkBot {
	return &LarkBot{
		WebhookURL: webhookURL,
	}
}

// SendMessage 方法用于向飞书机器人发送消息。它接受一个 message 字符串参数，并返回一个错误类型的值。
func (bot *LarkBot) SendMessage(message string) error {
	// 创建一个包含消息内容的字典
	data := map[string]interface{}{
		"msg_type": "text", // 消息类型为文本
		"content": map[string]string{
			"text": message, // 消息内容
		},
	}

	// 将字典编码为 JSON 格式
	body, err := json.Marshal(data)
	if err != nil {
		// 如果 JSON 编码失败，使用 logrus 记录错误并返回一个格式化的错误
		logrus.Errorf("Failed to marshal JSON: %v", err)
		return err
	}

	// 使用 http.Post 方法发送 POST 请求，内容类型为 "application/json"
	resp, err := http.Post(bot.WebhookURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		// 如果发送请求失败，使用 logrus 记录错误并返回一个格式化的错误
		logrus.Errorf("Failed to send message: %v", err)
		return err
	}
	defer resp.Body.Close() // 确保响应体在函数退出时关闭

	// 检查响应状态码是否为 200 OK
	if resp.StatusCode != http.StatusOK {
		// 如果状态码不是 200，使用 logrus 记录错误并返回一个格式化的错误
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		logrus.Error(err)
		return err
	}

	// 使用 logrus 记录成功发送消息的信息
	logrus.Infof("Message sent successfully: %s", message)

	// 如果没有错误，返回 nil
	return nil
}
