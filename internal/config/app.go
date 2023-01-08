package config

import (
	"context"
	"os"

	"github.com/pkg/errors"
	"github.com/sethvargo/go-envconfig"
	"github.com/spf13/viper"
)

type Config struct {
	Telegram struct {
		Bot struct {
			Token string `mapstructure:"token" env:"TELEGRAM_BOT_TOKEN"`
		} `mapstructure:"bot"`
		App struct {
			ID         int    `mapstructure:"id" env:"TELEGRAM_APP_ID"`
			Hash       string `mapstructure:"hash" env:"TELEGRAM_APP_HASH"`
			SessionDir string `mapstructure:"session_dir" env:"TELEGRAM_APP_SESSION_DIR"`
		} `mapstructure:"app"`
	} `mapstructure:"telegram"`
}

func NewConfig(ctx context.Context, configPath string) (*Config, error) {
	var conf Config
	if len(configPath) == 0 {
		if err := envconfig.Process(ctx, &conf); err != nil {
			return nil, errors.Wrap(err, "failed to process config environment variables")
		}
		return &conf, nil
	}

	f, err := os.Open(configPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open config file '%s'", configPath)
	}

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(f); err != nil {
		return nil, errors.Wrap(err, "failed to read config yaml file")
	}
	if err := v.Unmarshal(&conf); err != nil {
		return nil, errors.Wrap(err, "failed to decode config yaml file")
	}

	return &conf, nil
}
