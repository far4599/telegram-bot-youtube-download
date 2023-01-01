package context

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func NewSignalledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		defer cancel()

		done := make(chan os.Signal, 1)
		signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
		for {
			select {
			case <-done:
				return
			}
		}
	}()

	return ctx
}
