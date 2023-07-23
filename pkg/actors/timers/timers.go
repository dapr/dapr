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

package timers

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/internal"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.timers")

type timersMetricsCollector = func(actorType string, timers int64)

// Implements a timers provider.
type timers struct {
	clock                 clock.WithTicker
	executeTimerFn        internal.ExecuteTimerFn
	activeTimers          *sync.Map
	activeTimersCount     map[string]*int64
	activeTimersCountLock sync.RWMutex
	metricsCollector      timersMetricsCollector
	runningCh             chan struct{}
}

// NewTimersProvider returns a TimerProvider.
func NewTimersProvider(clock clock.WithTicker) internal.TimersProvider {
	return &timers{
		clock:             clock,
		activeTimers:      &sync.Map{},
		activeTimersCount: make(map[string]*int64),
		metricsCollector:  diag.DefaultMonitoring.ActorTimers,
		runningCh:         make(chan struct{}),
	}
}

func (t *timers) Init(ctx context.Context) error {
	return nil
}

func (t *timers) Close() error {
	// Close the runningCh
	close(t.runningCh)
	return nil
}

func (t *timers) SetExecuteTimerFn(fn internal.ExecuteTimerFn) {
	t.executeTimerFn = fn
}

func (t *timers) SetMetricsCollector(fn timersMetricsCollector) {
	t.metricsCollector = fn
}

func (t *timers) CreateTimer(ctx context.Context, reminder *internal.Reminder) error {
	timerKey := reminder.Key()

	log.Debugf("Create timer: %s", reminder.String())

	// Multiple goroutines could be trying to store this timer, so we need to repeat until we succeed or context is canceled
	stop := make(chan struct{}, 1)
	for {
		_, loaded := t.activeTimers.LoadOrStore(timerKey, stop)
		if !loaded {
			// If we stored the value, all good - let's continue
			break
		}

		// If there's already a timer with the same key, stop it so we can replace it
		prev, loaded := t.activeTimers.LoadAndDelete(timerKey)
		if loaded && prev != nil {
			close(prev.(chan struct{}))
		}

		// Wait a bit (with some jitter) and re-try
		select {
		case <-time.After(time.Duration(30*rand.Float32()) * time.Millisecond): //nolint:gosec
			// Can re-try
		case <-ctx.Done():
			return fmt.Errorf("failed to create timer: %w", ctx.Err())
		}
	}

	t.updateActiveTimersCount(reminder.ActorType, 1)

	go func() {
		var nextTimer clock.Timer

		nextTick, active := reminder.NextTick()
		if !active {
			log.Infof("Timer %s has expired", timerKey)
			goto delete
		}

		nextTimer = t.clock.NewTimer(nextTick.Sub(t.clock.Now()))
		defer func() {
			if nextTimer != nil && !nextTimer.Stop() {
				<-nextTimer.C()
			}
		}()

	loop:
		for {
			select {
			case <-nextTimer.C():
				// noop
			case <-stop:
				// timer has been already deleted
				log.Infof("Timer has been deleted: %s", reminder)
				break loop
			case <-t.runningCh:
				// Timers runtime is stopping
				return
			}

			// If executeTimerFn returns false, it means that the actor was stopped so it should not be fired again
			if t.executeTimerFn != nil && !t.executeTimerFn(reminder) {
				nextTimer = nil
				break loop
			}

			if reminder.TickExecuted() {
				log.Infof("Timer %s has been completed", timerKey)
				nextTimer = nil
				break loop
			}

			nextTick, active = reminder.NextTick()
			if !active {
				log.Infof("Timer %s has expired", timerKey)
				nextTimer = nil
				break loop
			}
			nextTimer.Reset(nextTick.Sub(t.clock.Now()))
		}

	delete:
		// Delete the timer from the table
		// We can't just call `DeleteTimer` as that could cause a race condition if the timer is also being replaced
		exists := t.activeTimers.CompareAndDelete(timerKey, stop)
		if exists {
			// We close the stop channel only if it was still in the map
			// If it isn't in the map, it means that someone else called DeleteTimer already, so the channel is already closed
			close(stop)
		}
		t.updateActiveTimersCount(reminder.ActorType, -1)
	}()
	return nil
}

func (t *timers) DeleteTimer(ctx context.Context, timerKey string) error {
	stopChan, exists := t.activeTimers.LoadAndDelete(timerKey)
	if exists {
		close(stopChan.(chan struct{}))
	}

	return nil
}

func (t *timers) updateActiveTimersCount(actorType string, inc int64) {
	t.activeTimersCountLock.RLock()
	_, ok := t.activeTimersCount[actorType]
	t.activeTimersCountLock.RUnlock()
	if !ok {
		t.activeTimersCountLock.Lock()
		if _, ok = t.activeTimersCount[actorType]; !ok { // re-check
			t.activeTimersCount[actorType] = new(int64)
		}
		t.activeTimersCountLock.Unlock()
	}

	newVal := atomic.AddInt64(t.activeTimersCount[actorType], inc)
	t.metricsCollector(actorType, newVal)
}

func (t *timers) GetActiveTimersCount(actorKey string) int64 {
	t.activeTimersCountLock.RLock()
	defer t.activeTimersCountLock.RUnlock()

	val := t.activeTimersCount[actorKey]
	if val == nil {
		return 0
	}

	return atomic.LoadInt64(val)
}
