package telegram

import (
	"time"

	"gopkg.in/tucnak/telebot.v3"
)

type BotClient struct {
	bot *telebot.Bot
}

func NewBotClient(token string) (*BotClient, error) {
	pref := telebot.Settings{
		Token:  token,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
	}

	bot, err := telebot.NewBot(pref)
	if err != nil {
		return nil, err
	}

	return &BotClient{
		bot: bot,
	}, nil
}

func (c *BotClient) Bot() *telebot.Bot {
	return c.bot
}
