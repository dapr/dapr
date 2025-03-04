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

package inmemory

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/engine"
	"github.com/dapr/dapr/pkg/actors/internal/timers"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/kit/events/queue"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.timers.inmemory")

type Options struct {
	Engine engine.Interface
}

type inmemory struct {
	clock clock.WithTicker

	engine                engine.Interface
	activeTimers          *sync.Map
	activeTimersCount     map[string]*int64
	activeTimersCountLock sync.RWMutex
	runningCh             chan struct{}
	processor             *queue.Processor[string, *api.Reminder]
}

// New returns a TimerProvider.
func New(opts Options) timers.Storage {
	i := &inmemory{
		engine:            opts.Engine,
		clock:             clock.RealClock{},
		activeTimers:      &sync.Map{},
		activeTimersCount: make(map[string]*int64),
		runningCh:         make(chan struct{}),
	}
	i.processor = queue.NewProcessor[string, *api.Reminder](i.processorExecuteFn)
	return i
}

func (i *inmemory) Close() error {
	// Close the runningCh and stop the processor
	close(i.runningCh)
	i.processor.Close()
	return nil
}

// processorExecuteFn is invoked when the processor executes a reminder.
func (i *inmemory) processorExecuteFn(reminder *api.Reminder) {
	err := i.engine.CallReminder(context.TODO(), reminder)
	diag.DefaultMonitoring.ActorTimerFired(reminder.ActorType, err == nil)
	if err != nil {
		// Successful and non-successful executions are treated as the same in
		// terms of ticking forward, so we log the error and continue.
		log.Errorf("Error executing timer: %s", err)
	}

	// If TickExecuted returns true, it means the timer has no more repetitions left
	if reminder.TickExecuted() {
		log.Infof("Timer %s has been completed", reminder.Key())
		if i.activeTimers.CompareAndDelete(reminder.Key(), reminder) {
			i.updateActiveTimersCount(reminder.ActorType, -1)
		}
		return
	}

	// If active is false, then the timer has expired
	if _, active := reminder.NextTick(); !active {
		log.Infof("Timer %s has expired", reminder.Key())
		if i.activeTimers.CompareAndDelete(reminder.Key(), reminder) {
			i.updateActiveTimersCount(reminder.ActorType, -1)
		}
		return
	}

	// Re-enqueue the timer for its next repetition
	i.processor.Enqueue(reminder)
}

func (i *inmemory) Create(ctx context.Context, reminder *api.Reminder) error {
	timerKey := reminder.Key()

	log.Debugf("Create timer: %s", reminder.String())

	// Multiple goroutines could be trying to store this timer, so we need to repeat until we succeed or context is canceled
	for {
		_, loaded := i.activeTimers.LoadOrStore(timerKey, reminder)
		if !loaded {
			// If we stored the value, all good - let's continue
			break
		}

		// If there's already a timer with the same key, stop it so we can replace it
		prev, loaded := i.activeTimers.LoadAndDelete(timerKey)
		if loaded && prev != nil {
			i.processor.Dequeue(prev.(*api.Reminder).Key())
			i.updateActiveTimersCount(reminder.ActorType, -1)
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
		i.removeTimerMatching(reminder)
		return nil
	}

	i.processor.Enqueue(reminder)
	i.updateActiveTimersCount(reminder.ActorType, 1)

	return nil
}

// removeTimerMatching removes a timer from the processor by removing a Reminder objec.
// This is different from DeleteTimer as it removes the timer only if it's the same object.
// It is used by CreateTimer.
func (i *inmemory) removeTimerMatching(reminder *api.Reminder) {
	// Delete the timer from the table
	// We can't just call `DeleteTimer` as that could cause a race condition if the timer is also being replaced
	key := reminder.Key()
	if i.activeTimers.CompareAndDelete(key, reminder) {
		i.processor.Dequeue(key)
	}
}

func (i *inmemory) Delete(_ context.Context, timerKey string) {
	reminderAny, exists := i.activeTimers.LoadAndDelete(timerKey)
	if exists {
		reminder := reminderAny.(*api.Reminder)
		i.updateActiveTimersCount(reminder.ActorType, -1)
		i.processor.Dequeue(reminder.Key())
	}
}

func (i *inmemory) updateActiveTimersCount(actorType string, inc int64) {
	i.activeTimersCountLock.RLock()
	_, ok := i.activeTimersCount[actorType]
	i.activeTimersCountLock.RUnlock()
	if !ok {
		i.activeTimersCountLock.Lock()
		if _, ok = i.activeTimersCount[actorType]; !ok { // re-check
			i.activeTimersCount[actorType] = new(int64)
		}
		i.activeTimersCountLock.Unlock()
	}

	newVal := atomic.AddInt64(i.activeTimersCount[actorType], inc)
	diag.DefaultMonitoring.ActorTimers(actorType, newVal)
}

func (i *inmemory) GetActiveTimersCount(actorKey string) int64 {
	i.activeTimersCountLock.RLock()
	defer i.activeTimersCountLock.RUnlock()

	val := i.activeTimersCount[actorKey]
	if val == nil {
		return 0
	}

	return atomic.LoadInt64(val)
}
