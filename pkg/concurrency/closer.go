/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package concurrency

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/kit/logger"
)

var (
	ErrManagerAlreadyClosed = errors.New("runner manager already closed")

	log = logger.NewLogger("dapr.concurrency.closer")
)

// RunnerCloserManager is a RunnerManager that also implements Closing of the
// added closers once the main runners are done.
type RunnerCloserManager struct {
	// mngr implements the main RunnerManager.
	mngr *RunnerManager

	// closers are the closers to be closed once the main runners are done.
	closers []func() error

	// retErr is the error returned by the main runners and closers. Used to
	// return the resulting error from Close().
	retErr error

	// fatalShutdownFn is called if the grace period is exceeded.
	// Defined if the grace period is not nil.
	fatalShutdownFn func()

	// closeFatalShutdown closes the fatal shutdown goroutine. Closing is a no-op
	// if fatalShutdownFn is nil.
	closeFatalShutdown chan struct{}

	clock   clock.Clock
	running atomic.Bool
	closing atomic.Bool
	closed  atomic.Bool
	closeCh chan struct{}
	stopped chan struct{}
}

// NewRunnerCloserManager creates a new RunnerCloserManager with the given
// grace period and runners.
// If gracePeriod is nil, the grace period is infinite.
func NewRunnerCloserManager(gracePeriod *time.Duration, runners ...Runner) *RunnerCloserManager {
	c := &RunnerCloserManager{
		mngr:               NewRunnerManager(runners...),
		clock:              clock.RealClock{},
		stopped:            make(chan struct{}),
		closeCh:            make(chan struct{}),
		closeFatalShutdown: make(chan struct{}),
	}

	if gracePeriod == nil {
		log.Warn("Graceful shutdown timeout is infinite, will wait indefinitely to shutdown")
		return c
	}

	c.fatalShutdownFn = func() {
		log.Fatal("Graceful shutdown timeout exceeded, forcing shutdown")
	}

	c.AddCloser(func() {
		log.Debugf("Graceful shutdown timeout: %s", *gracePeriod)

		t := c.clock.NewTimer(*gracePeriod)
		defer t.Stop()

		select {
		case <-t.C():
			c.fatalShutdownFn()
		case <-c.closeFatalShutdown:
		}
	})

	return c
}

// Add implements RunnerManager.Add.
func (c *RunnerCloserManager) Add(runner ...Runner) error {
	if c.running.Load() {
		return ErrManagerAlreadyStarted
	}

	return c.mngr.Add(runner...)
}

// AddCloser adds a closer to the list of closers to be closed once the main
// runners are done.
func (c *RunnerCloserManager) AddCloser(closers ...any) error {
	if c.closing.Load() {
		return ErrManagerAlreadyClosed
	}

	c.mngr.lock.Lock()
	defer c.mngr.lock.Unlock()

	var errs []error
	for _, cl := range closers {
		switch v := cl.(type) {
		case io.Closer:
			c.closers = append(c.closers, v.Close)
		case func(context.Context) error:
			c.closers = append(c.closers, func() error {
				// We use a background context here since the fatalShutdownFn will kill
				// the program if the grace period is exceeded.
				return v(context.Background())
			})
		case func() error:
			c.closers = append(c.closers, v)
		case func():
			c.closers = append(c.closers, func() error {
				v()
				return nil
			})
		default:
			errs = append(errs, fmt.Errorf("unsupported closer type: %T", v))
		}
	}

	return errors.Join(errs...)
}

// Add implements RunnerManager.Run.
func (c *RunnerCloserManager) Run(ctx context.Context) error {
	if !c.running.CompareAndSwap(false, true) {
		return ErrManagerAlreadyStarted
	}

	// Signal the manager is stopped.
	defer close(c.stopped)

	// If the main runner has at least one runner, add a closer that will
	// close the context once Close() is called.
	if len(c.mngr.runners) > 0 {
		c.mngr.Add(func(ctx context.Context) error {
			select {
			case <-ctx.Done():
			case <-c.closeCh:
			}
			return nil
		})
	}

	errCh := make(chan error, len(c.closers))
	go func() {
		errCh <- c.mngr.Run(ctx)
	}()

	rErr := <-errCh

	c.mngr.lock.Lock()
	defer c.mngr.lock.Unlock()
	c.closing.Store(true)

	errs := make([]error, len(c.closers)+1)
	errs[0] = rErr

	for _, closer := range c.closers {
		go func(closer func() error) {
			errCh <- closer()
		}(closer)
	}

	// Wait for all closers to be done.
	for i := 1; i < len(c.closers)+1; i++ {
		// Close the fatal shutdown goroutine if all closers are done. This is a
		// no-op if the fatal go routine is not defined.
		if i == len(c.closers) {
			close(c.closeFatalShutdown)
		}
		errs[i] = <-errCh
	}

	c.retErr = errors.Join(errs...)

	return c.retErr
}

// Close will close the main runners and then the closers.
func (c *RunnerCloserManager) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		close(c.closeCh)
	}
	// If the manager is not running yet, we stop immediately.
	if c.running.CompareAndSwap(false, true) {
		close(c.stopped)
	}
	c.WaitUntilShutdown()
	return c.retErr
}

// WaitUntilShutdown will block until the main runners and closers are done.
func (c *RunnerCloserManager) WaitUntilShutdown() {
	<-c.stopped
}
