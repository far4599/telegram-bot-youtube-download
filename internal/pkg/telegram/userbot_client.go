package telegram

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cenkalti/backoff/v4"
	"github.com/far4599/telegram-bot-youtube-download/internal/config"
	"github.com/far4599/telegram-bot-youtube-download/internal/models"
	"github.com/far4599/telegram-bot-youtube-download/internal/pkg/log"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/pkg/errors"
)

type UserBotClient struct {
	conf *config.Config

	client *telegram.Client
}

func NewUserBotClient(ctx context.Context, conf *config.Config) *UserBotClient {
	c := &UserBotClient{
		conf: conf,
	}

	go func() {
		if err := c.run(ctx); err != nil {
			log.Logger.Fatal(err)
		}
	}()

	return c
}

func (c *UserBotClient) run(ctx context.Context) error {
	sessionDir := c.conf.Telegram.App.SessionDir
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		return errors.Wrapf(err, "failed to create sessions dir '%s'", sessionDir)
	}

	opts := telegram.Options{
		Logger: log.Logger.Desugar(),
		ReconnectionBackoff: func() backoff.BackOff {
			return backoff.NewExponentialBackOff()
		},
		NoUpdates: true,
		SessionStorage: &session.FileStorage{
			Path: filepath.Join(sessionDir, "session.json"),
		},
	}

	c.client = telegram.NewClient(c.conf.Telegram.App.ID, c.conf.Telegram.App.Hash, opts)

	return c.client.Run(ctx, func(ctx context.Context) error {
		status, err := c.client.Auth().Status(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to get auth status")
		}

		if !status.Authorized {
			if _, err = c.client.Auth().Bot(ctx, c.conf.Telegram.Bot.Token); err != nil {
				return errors.Wrap(err, "failed to login as userbot")
			}
		}

		log.Logger.Info("userbot connected")

		return telegram.RunUntilCanceled(ctx, c.client)
	})
}

func (c *UserBotClient) UploadFile(ctx context.Context, to tg.InputPeerClass, videoOption *models.VideoOption, pipe io.Reader) error {
	api := tg.NewClient(c.client)
	u := uploader.NewUploader(api)
	s := message.NewSender(api).WithUploader(u)

	f, err := u.FromReader(ctx, videoOption.VideoInfo.Title, pipe)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to upload '%s'", videoOption.Path))
	}

	target := s.To(to)
	if target == nil {
		return nil
	}

	var md message.MediaOption
	if videoOption.Audio {
		md = message.Audio(f).
			Title(videoOption.VideoInfo.Title).
			Performer(videoOption.VideoInfo.URL).
			DurationSeconds(videoOption.VideoInfo.Duration)
	} else {
		md = message.Video(f, styling.Plain(videoOption.Label+" - "+videoOption.VideoInfo.Title))
	}

	_, err = target.Media(ctx, md)
	if err != nil {
		return err
	}

	return nil
}
