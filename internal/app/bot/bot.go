package bot

import (
	"context"
	"os"
	"path/filepath"

	"github.com/far4599/telegram-bot-youtube-download/internal/config"
	"github.com/far4599/telegram-bot-youtube-download/internal/pkg/telegram"
	"github.com/far4599/telegram-bot-youtube-download/internal/service"
	"github.com/gotd/td/session"
	"github.com/pkg/errors"
	"gopkg.in/telebot.v3"
)

type Bot struct {
	conf *config.Config

	tmh *service.TelegramMessageHandler
}

func NewApp(conf *config.Config, vs *service.VideoService) *Bot {
	return &Bot{
		conf: conf,
		tmh:  service.NewMessageHandler(conf, vs),
	}
}

func (b *Bot) Run(ctx context.Context) error {
	if err := b.run(ctx); err != nil {
		return err
	}

	return nil
}

func (b *Bot) run(ctx context.Context) error {
	userbot := telegram.NewUserBotClient(ctx, b.conf)

	bot, err := telegram.NewBotClient(b.conf.Telegram.Bot.Token)
	if err != nil {
		return err
	}

	err = b.setMessageHandlers(bot, userbot)
	if err != nil {
		return err
	}

	bot.Bot().Start()

	return nil
}

// func (b *Bot) run(ctx context.Context) error {
// 	dispatcher := tg.NewUpdateDispatcher()
// 	sessionStorage, err := b.newSessionStorage()
// 	if err != nil {
// 		return err
// 	}
//
// 	opts := telegram.Options{
// 		Logger:         log.Logger.Desugar(),
// 		UpdateHandler:  dispatcher,
// 		SessionStorage: sessionStorage,
// 		Resolver: dcs.Plain(dcs.PlainOptions{
// 			Dial: proxy.Dial,
// 		}),
// 	}
//
// 	client := telegram.NewClient(b.conf.Telegram.App.ID, b.conf.Telegram.App.Hash, opts)
//
// 	err = b.setMessageHandlers(client, dispatcher)
// 	if err != nil {
// 		return err
// 	}
//
// 	return client.Run(ctx, func(ctx context.Context) error {
// 		status, err := client.Auth().Status(ctx)
// 		if err != nil {
// 			return errors.Wrap(err, "auth status")
// 		}
//
// 		if !status.Authorized {
// 			if _, err := client.Auth().Bot(ctx, b.conf.Telegram.Bot.Token); err != nil {
// 				return errors.Wrap(err, "failed to login telegram bot")
// 			}
// 		}
//
// 		return telegram.RunUntilCanceled(ctx, client)
// 	})
// }

func (b *Bot) newSessionStorage() (*session.FileStorage, error) {
	sessionFile := filepath.Join(b.conf.Telegram.App.SessionDir, "session.json")
	if err := os.MkdirAll(b.conf.Telegram.App.SessionDir, 0700); err != nil {
		return nil, errors.Wrap(err, "failed to create session dir")
	}

	return &session.FileStorage{
		Path: sessionFile,
	}, nil
}

func (b *Bot) setMessageHandlers(botClient *telegram.BotClient, userbotClient *telegram.UserBotClient) error {
	// api := tg.NewClient(client)
	// dispatcher.OnNewMessage(b.tmh.OnNewMessage(api))
	bot := botClient.Bot()

	bot.Handle("/start", b.tmh.OnStart())
	bot.Handle(telebot.OnText, b.tmh.OnNewMessage())
	bot.Handle(telebot.OnCallback, b.tmh.OnCallback(userbotClient))

	return nil
}
