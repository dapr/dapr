package wfengine

import (
	"context"

	"github.com/microsoft/durabletask-go/backend"
)

func NewNoOpTaskWorker(logger backend.Logger) backend.TaskWorker {
	return &worker{
		logger: logger,
		cancel: nil,
	}
}

type worker struct {
	logger backend.Logger
	cancel context.CancelFunc
}

func (w *worker) Start(ctx context.Context) {
	w.logger.Debugf("No-op task worker - started")
	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
}

func (w *worker) ProcessNext(ctx context.Context) (bool, error) {
	w.logger.Debugf("No-op task worker - process next")
	return false, nil
}

func (w *worker) StopAndDrain() {
	w.logger.Debugf("No-op task worker - stopping")
	if w.cancel != nil {
		w.cancel()
	}
	w.logger.Debugf("No-op task worker - stopped")
}
