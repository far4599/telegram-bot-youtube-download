package main

import (
	"flag"

	"github.com/far4599/telegram-bot-youtube-download/internal/app"
	"github.com/far4599/telegram-bot-youtube-download/internal/config"
	"github.com/far4599/telegram-bot-youtube-download/internal/pkg/context"
	"github.com/far4599/telegram-bot-youtube-download/internal/pkg/log"
)

var flagConfigFile = flag.String("f", "", "path to configuration yaml file")

func main() {
	flag.Parse()

	ctx := context.NewSignalledContext()

	conf, err := config.NewConfig(ctx, *flagConfigFile)
	if err != nil {
		log.Logger.Fatalw("failed to load config", "error", err)
	}

	if err = app.NewApp(conf).Run(ctx); err != nil {
		log.Logger.Fatalw("app exited unexpectedly", "error", err)
	}
}
