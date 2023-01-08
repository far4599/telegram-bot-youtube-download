package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/far4599/telegram-bot-youtube-download/internal/config"
	"github.com/far4599/telegram-bot-youtube-download/internal/models"
	"github.com/far4599/telegram-bot-youtube-download/internal/pkg/log"
	"github.com/far4599/telegram-bot-youtube-download/internal/pkg/telegram"
	"github.com/gotd/td/tg"
	"gopkg.in/tucnak/telebot.v3"
)

const (
	videoEmoji = "🎥"
	audioEmoji = "🎧"
)

type TelegramMessageHandler struct {
	conf *config.Config

	vs *VideoService
}

func NewMessageHandler(conf *config.Config, vs *VideoService) *TelegramMessageHandler {
	return &TelegramMessageHandler{
		conf: conf,
		vs:   vs,
	}
}

func (h *TelegramMessageHandler) OnStart() telebot.HandlerFunc {
	return func(m telebot.Context) (err error) {
		m.Send("Send me a video link (YouTube, Vimeo, etc). You may also share a link from the streaming application. And I'll send you download options if streaming service is supported.")

		return nil
	}
}

func (h *TelegramMessageHandler) OnCallback(userbotClient *telegram.UserBotClient) telebot.HandlerFunc {
	return func(m telebot.Context) (err error) {
		defer func() {
			if err != nil {
				log.Logger.Error(err)
			}
		}()

		defer m.Respond()

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
		defer cancel()

		m.Notify(telebot.UploadingVideo)

		videoID := strings.TrimSpace(m.Callback().Data)

		videoOption, pipe, err := h.vs.DownloadVideo(ctx, videoID)
		if err != nil {
			return err
		}
		defer pipe.Close()

		err = userbotClient.UploadFile(ctx, &tg.InputPeerUser{UserID: m.Sender().ID}, videoOption, pipe)
		if err != nil {
			return err
		}

		return nil
	}
}

func (h *TelegramMessageHandler) OnNewMessage() telebot.HandlerFunc {
	return func(m telebot.Context) (err error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		tmpMsg, err := m.Bot().Send(m.Sender(), "gathering info")
		if err != nil {
			return err
		}
		defer func() {
			if err != nil {
				defer m.Bot().Send(m.Sender(), fmt.Sprintf("error: '%s'", err))
			}

			m.Bot().Delete(tmpMsg)
		}()

		m.Notify(telebot.FindingLocation)

		videoURL, err := fetchFirstURL(m.Text())
		if err != nil {
			videoURL = m.Text()
		}

		videoInfo, json, err := h.vs.GetVideoInfo(ctx, videoURL)
		if err != nil {
			return err
		}

		videoOpts, err := h.vs.GetVideoOptions(videoInfo, json)
		if err != nil {
			return err
		}

		msg, opts := createVideoInfoMessage(videoInfo, videoOpts)
		m.Send(msg, opts...)

		return err
	}
}

func createVideoInfoMessage(info *models.VideoInfo, opts []*models.VideoOption) (msg any, options []any) {
	if len(info.ThumbURL) > 0 {
		msg = &telebot.Photo{
			File: telebot.File{
				FileURL: info.ThumbURL,
			},
			Caption: info.Title,
		}
	} else {
		msg = info.Title
	}

	if len(opts) > 0 {
		inlineMenu := &telebot.ReplyMarkup{}

		rows := make([]telebot.Row, 0, len(opts))
		for _, opt := range opts {
			emoji := videoEmoji
			if opt.Audio {
				emoji = audioEmoji
			}

			title := emoji + " " + opt.Label + " (" + humanize.Bytes(opt.Size) + ")"

			rows = append(rows, inlineMenu.Row(inlineMenu.Data(title, opt.ID)))
		}

		inlineMenu.Inline(rows...)

		options = append(options, inlineMenu)
	}

	return
}

func fetchFirstURL(input string) (string, error) {
	regex := regexp.MustCompile(`^https?://[^\s"]+$`)

	match := regex.FindString(input)
	if match == "" {
		return "", ErrNotFound
	}

	return match, nil
}
