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

// SendMessage 方法用于向飞书机器人发送消息卡片。它接受 title 和内容参数，并返回一个错误类型的值。
func (bot *LarkBot) SendMessage(title, time, from, to, txHashFrom, txHashTo string) error {
	content := fmt.Sprintf("**Time:** %s\n\n**From:** %s\n**To:** %s\n\n**Tx hash (From):** %s\n**Tx hash (To):** %s\n",
		time, from, to, txHashFrom, txHashTo)

	data := map[string]interface{}{
		"msg_type": "interactive",
		"card": map[string]interface{}{
			"elements": []map[string]interface{}{
				{
					"tag": "div",
					"text": map[string]interface{}{
						"content": content,
						"tag":     "lark_md",
					},
				},
			},
			"header": map[string]interface{}{
				"title": map[string]interface{}{
					"content": title,
					"tag":     "plain_text",
				},
			},
		},
	}

	body, err := json.Marshal(data)
	if err != nil {
		logrus.Errorf("Failed to marshal JSON: %v", err)
		return err
	}

	resp, err := http.Post(bot.WebhookURL, "application/json", bytes.NewBuffer(body))
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

	logrus.Infof("Message sent successfully: %s", title)
	return nil
}
