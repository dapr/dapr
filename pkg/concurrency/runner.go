/*
Copyright 2023 The Dapr Authors
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
	"sync"
	"sync/atomic"
)

var ErrManagerAlreadyStarted = errors.New("manager already started")

// Runner is a function that runs a task.
type Runner func(context.Context) error

// RunnerManager is a manager for runners. It runs all runners in parallel and
// waits for all runners to finish. If any runner returns, the RunnerManager
// will stop all other runners and return any error.
type RunnerManager struct {
	lock    sync.Mutex
	runners []Runner
	running atomic.Bool
}

// NewRunnerManager creates a new RunnerManager.
func NewRunnerManager(runners ...Runner) *RunnerManager {
	return &RunnerManager{
		runners: runners,
	}
}

// Add adds a new runner to the RunnerManager.
func (r *RunnerManager) Add(runner ...Runner) error {
	if r.running.Load() {
		return ErrManagerAlreadyStarted
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	r.runners = append(r.runners, runner...)
	return nil
}

// Run runs all runners in parallel and waits for all runners to finish. If any
// runner returns, the RunnerManager will stop all other runners and return any
// error.
func (r *RunnerManager) Run(ctx context.Context) (err error) {
	if !r.running.CompareAndSwap(false, true) {
		return ErrManagerAlreadyStarted
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error)
	for _, runner := range r.runners {
		go func(runner Runner) {
			// Since the task returned, we need to cancel all other tasks.
			// This is a noop if the parent context is already cancelled, or another
			// task returned before this one.
			defer cancel()

			// Ignore context cancelled errors since errors from a runner manager
			// will likely determine the exit code of the program.
			// Context cancelled errors are also not really useful to the user in
			// this situation.
			rErr := runner(ctx)
			if rErr != nil && !errors.Is(rErr, context.Canceled) {
				errCh <- rErr
				return
			}
			errCh <- nil
		}(runner)
	}

	// Collect all errors
	for i := 0; i < len(r.runners); i++ {
		err = errors.Join(err, <-errCh)
	}

	return err
}
