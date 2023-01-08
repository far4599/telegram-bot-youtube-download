package log

import (
	"go.uber.org/zap"
)

var (
	// Logger = zap.Must(zap.NewProduction()).Sugar()
	Logger = zap.Must(zap.NewDevelopment()).Sugar()
)
