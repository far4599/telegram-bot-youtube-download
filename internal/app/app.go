package app

import (
	"context"

	"github.com/far4599/telegram-bot-youtube-download/internal/app/bot"
	"github.com/far4599/telegram-bot-youtube-download/internal/config"
	"github.com/far4599/telegram-bot-youtube-download/internal/repository"
	"github.com/far4599/telegram-bot-youtube-download/internal/service"
	"golang.org/x/sync/errgroup"
)

type App struct {
	conf *config.Config
}

func NewApp(conf *config.Config) *App {
	return &App{
		conf: conf,
	}
}

func (app *App) Run(ctx context.Context) error {
	inMemRepo, err := repository.NewInMemRepository()
	if err != nil {
		return err
	}

	vs, err := service.NewVideoService(2, inMemRepo)
	if err != nil {
		return err
	}

	errGroup, errCtx := errgroup.WithContext(ctx)

	// errGroup.Go(func() error {
	// 	s, _ := service.NewVideoService(2, inMemRepo)
	//
	// 	options, err := s.GetVideoOptions(errCtx, "https://youtube.com/shorts/baUkeYKZa9Y")
	// 	if err != nil {
	// 		return err
	// 	}
	//
	// 	fmt.Println(options)
	//
	// 	return nil
	// })

	errGroup.Go(func() error {
		return bot.NewApp(app.conf, vs).Run(errCtx)
	})

	return errGroup.Wait()
}
