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
	w.logger.Infof("No-Op Task Worker: Start...")
	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
}

func (w *worker) ProcessNext(ctx context.Context) (bool, error) {
	w.logger.Infof("No-Op Task Worker: Process Next...")
	return false, nil
}

func (w *worker) StopAndDrain() {
	w.logger.Infof("No-Op Task Worker: Stopping...")
	if w.cancel != nil {
		w.cancel()
	}
	w.logger.Infof("No-Op Task Worker: Stopped")
}
