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
	"github.com/dapr/kit/events/queue"
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
	processor             *queue.Processor[string, *internal.Reminder]
}

// NewTimersProvider returns a TimerProvider.
func NewTimersProvider(clock clock.WithTicker) internal.TimersProvider {
	t := &timers{
		clock:             clock,
		activeTimers:      &sync.Map{},
		activeTimersCount: make(map[string]*int64),
		metricsCollector:  diag.DefaultMonitoring.ActorTimers,
		runningCh:         make(chan struct{}),
	}
	t.processor = queue.NewProcessor[string, *internal.Reminder](t.processorExecuteFn)
	return t
}

func (t *timers) Init(ctx context.Context) error {
	return nil
}

func (t *timers) Close() error {
	// Close the runningCh and stop the processor
	close(t.runningCh)
	return t.processor.Close()
}

func (t *timers) SetExecuteTimerFn(fn internal.ExecuteTimerFn) {
	t.executeTimerFn = fn
}

func (t *timers) SetMetricsCollector(fn timersMetricsCollector) {
	t.metricsCollector = fn
}

// processorExecuteFn is invoked when the processor executes a reminder.
func (t *timers) processorExecuteFn(reminder *internal.Reminder) {
	// If executeTimerFn returns false, it means that the actor was stopped so it should not be fired again
	if t.executeTimerFn != nil && !t.executeTimerFn(reminder) {
		if t.activeTimers.CompareAndDelete(reminder.Key(), reminder) {
			t.updateActiveTimersCount(reminder.ActorType, -1)
		}
		return
	}

	// If TickExecuted returns true, it means the timer has no more repetitions left
	if reminder.TickExecuted() {
		log.Infof("Timer %s has been completed", reminder.Key())
		if t.activeTimers.CompareAndDelete(reminder.Key(), reminder) {
			t.updateActiveTimersCount(reminder.ActorType, -1)
		}
		return
	}

	// If active is false, then the timer has expired
	if _, active := reminder.NextTick(); !active {
		log.Infof("Timer %s has expired", reminder.Key())
		if t.activeTimers.CompareAndDelete(reminder.Key(), reminder) {
			t.updateActiveTimersCount(reminder.ActorType, -1)
		}
		return
	}

	// Re-enqueue the timer for its next repetition
	t.processor.Enqueue(reminder)
}

func (t *timers) CreateTimer(ctx context.Context, reminder *internal.Reminder) error {
	timerKey := reminder.Key()

	log.Debugf("Create timer: %s", reminder.String())

	// Multiple goroutines could be trying to store this timer, so we need to repeat until we succeed or context is canceled
	for {
		_, loaded := t.activeTimers.LoadOrStore(timerKey, reminder)
		if !loaded {
			// If we stored the value, all good - let's continue
			break
		}

		// If there's already a timer with the same key, stop it so we can replace it
		prev, loaded := t.activeTimers.LoadAndDelete(timerKey)
		if loaded && prev != nil {
			t.processor.Dequeue(prev.(*internal.Reminder).Key())
			t.updateActiveTimersCount(reminder.ActorType, -1)
		}

		// Wait a bit (with some jitter) and re-try
		select {
		case <-time.After(time.Duration(30*rand.Float32()) * time.Millisecond): //nolint:gosec
			// Can re-try
		case <-ctx.Done():
			return fmt.Errorf("failed to create timer: %w", ctx.Err())
		}
	}

	// Check if the reminder hasn't expired, then enqueue it
	_, active := reminder.NextTick()
	if !active {
		log.Infof("Timer %s has expired", timerKey)
		t.removeTimerMatching(reminder)
		return nil
	}

	err := t.processor.Enqueue(reminder)
	if err != nil {
		t.removeTimerMatching(reminder)
		return fmt.Errorf("failed to enqueue timer: %w", err)
	}

	t.updateActiveTimersCount(reminder.ActorType, 1)

	return nil
}

// removeTimerMatching removes a timer from the processor by removing a Reminder object.
// This is different from DeleteTimer as it removes the timer only if it's the same object.
// It is used by CreateTimer.
func (t *timers) removeTimerMatching(reminder *internal.Reminder) {
	// Delete the timer from the table
	// We can't just call `DeleteTimer` as that could cause a race condition if the timer is also being replaced
	key := reminder.Key()
	if t.activeTimers.CompareAndDelete(key, reminder) {
		t.processor.Dequeue(key)
	}
}

func (t *timers) DeleteTimer(_ context.Context, timerKey string) error {
	reminderAny, exists := t.activeTimers.LoadAndDelete(timerKey)
	if exists {
		reminder := reminderAny.(*internal.Reminder)
		t.updateActiveTimersCount(reminder.ActorType, -1)
		err := t.processor.Dequeue(reminder.Key())
		if err != nil {
			return err
		}
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
