package telegram

import (
	"context"
	"sync"

	"github.com/gotd/td/telegram/uploader"
	"go.uber.org/atomic"
)

type UploaderProgress struct {
	progress         *atomic.Int32 // 0 - 100
	progressChangeCh chan int32

	closed bool
	mu     sync.Mutex
}

func NewUploaderProgress() *UploaderProgress {
	return &UploaderProgress{
		progress:         atomic.NewInt32(-1),
		progressChangeCh: make(chan int32, 101),
	}
}

func (up *UploaderProgress) Chunk(_ context.Context, state uploader.ProgressState) error {
	newProgress := int32(state.Uploaded / (state.Total / 100))

	if newProgress > 100 {
		newProgress = 100
	}

	if up.progress.Load() == newProgress {
		return nil
	}

	up.progress.Store(newProgress)

	up.mu.Lock()
	defer up.mu.Unlock()

	if !up.closed {
		up.progressChangeCh <- newProgress
	}

	return nil
}

func (up *UploaderProgress) ProgressChan() <-chan int32 {
	return up.progressChangeCh
}

func (up *UploaderProgress) Close() {
	up.mu.Lock()
	defer up.mu.Unlock()

	up.closed = true
	close(up.progressChangeCh)
}
