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
	"sync"
	"sync/atomic"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/timers"
	"github.com/dapr/dapr/pkg/actors/router"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/kit/events/queue"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.timers.inmemory")

// defaultMaxConcurrency bounds concurrent timer callbacks when
// Options.MaxConcurrency is unset, capping goroutine growth under a backlog of
// slow callbacks. Matches defaultBulkPublishMaxConcurrency.
const defaultMaxConcurrency = 100

type Options struct {
	Router router.Interface

	// MaxConcurrency is the maximum number of timer callbacks that may execute
	// concurrently. Values <= 0 use defaultMaxConcurrency.
	MaxConcurrency int
}

type inmemory struct {
	clock clock.WithTicker

	router                router.Interface
	activeTimers          *sync.Map
	activeTimersCount     map[string]*int64
	activeTimersCountLock sync.RWMutex
	processor             *queue.Processor[string, *api.Reminder]

	// queueLock keeps activeTimers and the processor queue consistent: Create,
	// Delete, and the callback re-enqueue hold it so their paired mutations don't
	// interleave.
	queueLock sync.Mutex

	sem    chan struct{}   // bounds concurrent callbacks
	wg     sync.WaitGroup  // tracks in-flight callbacks so Close can drain them
	ctx    context.Context // cancelled by Close to abort in-flight callbacks
	cancel context.CancelFunc
}

// New returns a TimerProvider.
func New(opts Options) timers.Storage {
	maxConcurrency := opts.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = defaultMaxConcurrency
	}

	ctx, cancel := context.WithCancel(context.Background())
	i := &inmemory{
		router:            opts.Router,
		clock:             clock.RealClock{},
		activeTimers:      &sync.Map{},
		activeTimersCount: make(map[string]*int64),
		sem:               make(chan struct{}, maxConcurrency),
		ctx:               ctx,
		cancel:            cancel,
	}
	i.processor = queue.NewProcessor[string, *api.Reminder](queue.Options[string, *api.Reminder]{
		ExecuteFn: i.processorExecuteFn,
	})
	return i
}

func (i *inmemory) Close() error {
	// Cancel in-flight callbacks first. This both aborts running callbacks and
	// unblocks any processorExecuteFn parked on the concurrency semaphore;
	// otherwise processor.Close() (which waits for the processing loop to exit)
	// could deadlock when the pool is full at shutdown.
	i.cancel()
	i.processor.Close()
	// Drain any callback goroutines that are still finishing.
	i.wg.Wait()
	return nil
}

// processorExecuteFn is invoked by the queue processor when a timer is due. It
// dispatches the callback to a bounded pool so callbacks for different actors
// run concurrently. Per-actor serialization is still enforced downstream by the
// per-actor lock (pkg/actors/targets/app/lock); that lock provides mutual
// exclusion, not ordering, so an actor's timers may now fire in any order.
func (i *inmemory) processorExecuteFn(reminder *api.Reminder) {
	// A full pool parks the processing loop: backpressure plus a bound on
	// goroutine growth.
	select {
	case i.sem <- struct{}{}:
	case <-i.ctx.Done():
		return
	}

	i.wg.Add(1)
	go func() {
		defer func() {
			<-i.sem
			i.wg.Done()
		}()
		i.executeAndReschedule(reminder)
	}()
}

// executeAndReschedule invokes a single timer callback and, if the timer
// repeats, re-enqueues its next tick. It runs on a pool goroutine. Because the
// next tick is only enqueued after the callback returns, a repeating timer never
// overlaps its own next firing (dapr/dapr#1026).
func (i *inmemory) executeAndReschedule(reminder *api.Reminder) {
	err := i.router.CallReminder(i.ctx, reminder)
	diag.DefaultMonitoring.ActorTimerFired(reminder.ActorType, err == nil)
	if err != nil {
		// Successful and non-successful executions are treated as the same in
		// terms of ticking forward, so we log the error and continue.
		log.Errorf("Error executing timer: %s", err)
	}

	// Advance the schedule on a copy: the processor reads ScheduledTime()
	// (RegisteredTime) as its heap key from another goroutine, so mutating the
	// reminder in place would race those reads. Publish the copy instead.
	next := *reminder
	done := next.TickExecuted()
	_, active := next.NextTick()

	// Atomic with Create/Delete: without queueLock a concurrent delete or replace
	// between the check and the Enqueue could resurrect or lose a timer.
	i.queueLock.Lock()
	switch {
	case done || !active:
		// No repetitions left, or expired: drop it.
		if i.activeTimers.CompareAndDelete(reminder.Key(), reminder) {
			i.updateActiveTimersCount(reminder.ActorType, -1)
		}
	default:
		// Re-enqueue only if this is still the active timer for the key.
		if cur, ok := i.activeTimers.Load(reminder.Key()); ok && cur == reminder {
			i.activeTimers.Store(reminder.Key(), &next)
			i.processor.Enqueue(&next)
		}
	}
	i.queueLock.Unlock()

	switch {
	case done:
		log.Infof("Timer %s has been completed", reminder.Key())
	case !active:
		log.Infof("Timer %s has expired", reminder.Key())
	}
}

func (i *inmemory) Create(_ context.Context, reminder *api.Reminder) error {
	timerKey := reminder.Key()

	log.Debugf("Create timer: %s", reminder.String())

	_, active := reminder.NextTick()

	// queueLock makes the replace atomic, superseding the previous
	// spin-retry-on-sync.Map-contention loop.
	i.queueLock.Lock()
	defer i.queueLock.Unlock()

	// If there's already a timer with the same key, stop it so we can replace it.
	if prev, loaded := i.activeTimers.LoadAndDelete(timerKey); loaded && prev != nil {
		i.processor.Dequeue(prev.(*api.Reminder).Key())
		i.updateActiveTimersCount(reminder.ActorType, -1)
	}

	// If the reminder has already expired, leave it removed and don't enqueue.
	if !active {
		log.Infof("Timer %s has expired", timerKey)
		return nil
	}

	i.activeTimers.Store(timerKey, reminder)
	i.processor.Enqueue(reminder)
	i.updateActiveTimersCount(reminder.ActorType, 1)

	return nil
}

func (i *inmemory) Delete(_ context.Context, timerKey string) {
	i.queueLock.Lock()
	defer i.queueLock.Unlock()

	reminderAny, exists := i.activeTimers.LoadAndDelete(timerKey)
	if exists {
		reminder := reminderAny.(*api.Reminder)
		i.processor.Dequeue(reminder.Key())
		i.updateActiveTimersCount(reminder.ActorType, -1)
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
