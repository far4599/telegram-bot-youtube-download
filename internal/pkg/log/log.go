package log

import (
	"os"
	"strings"

	"go.uber.org/zap"
)

var (
	Logger = zap.Must(zap.NewProduction()).Sugar()
)

func init() {
	if strings.ToLower(os.Getenv("DEBUG")) == "true" {
		Logger = zap.Must(zap.NewDevelopment()).Sugar()
	}
}
