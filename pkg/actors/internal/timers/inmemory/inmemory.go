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
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/events/queue"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.timers.inmemory")

// loopFactory builds the per-actor execution loops. A single queue.Processor
// (a min-heap keyed by fire time) schedules all timers; when one is due it is
// routed to its actor's loop, which runs that actor's callbacks serially and in
// scheduled order. Callbacks for different actors run on their own loops
// concurrently, so a slow callback never blocks another actor's timers.
var loopFactory = loop.New[timerEvent](8)

// timerEvent is the sealed set of events handled by an actor's timer loop.
type timerEvent interface{ isTimerEvent() }

// eventFire executes a due timer callback.
type eventFire struct{ reminder *api.Reminder }

func (*eventFire) isTimerEvent() {}

// eventStop drains and stops an actor loop; enqueued by Close.
type eventStop struct{}

func (*eventStop) isTimerEvent() {}

type Options struct {
	Router router.Interface
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

	// actorLoops holds one serial execution loop per actor, created lazily when
	// an actor's first timer fires. Loops are not torn down while idle; they are
	// closed together on Close.
	actorLoops     map[string]loop.Interface[timerEvent]
	actorLoopsLock sync.Mutex
	closed         bool

	wg     sync.WaitGroup  // tracks actor-loop goroutines so Close can drain them
	ctx    context.Context // cancelled by Close to abort in-flight callbacks
	cancel context.CancelFunc
}

// New returns a TimerProvider.
func New(opts Options) timers.Storage {
	ctx, cancel := context.WithCancel(context.Background())
	i := &inmemory{
		router:            opts.Router,
		clock:             clock.RealClock{},
		activeTimers:      &sync.Map{},
		activeTimersCount: make(map[string]*int64),
		actorLoops:        make(map[string]loop.Interface[timerEvent]),
		ctx:               ctx,
		cancel:            cancel,
	}
	i.processor = queue.NewProcessor[string, *api.Reminder](queue.Options[string, *api.Reminder]{
		ExecuteFn: i.processorExecuteFn,
	})
	return i
}

func (i *inmemory) Close() error {
	// Abort in-flight callbacks (the context each loop passes to CallReminder).
	i.cancel()
	// Stop the scheduler before the loops: no further fires must be routed once
	// we start closing loops, otherwise Enqueue could send on a closed loop.
	i.processor.Close()

	i.actorLoopsLock.Lock()
	i.closed = true
	loops := i.actorLoops
	i.actorLoops = nil
	i.actorLoopsLock.Unlock()

	for _, l := range loops {
		l.Close(&eventStop{})
	}
	i.wg.Wait()
	return nil
}

// processorExecuteFn is invoked by the scheduler when a timer is due. It routes
// the fire to the actor's serial loop, so an actor's callbacks run in scheduled
// order while different actors run concurrently. Enqueue is non-blocking, so a
// slow callback never stalls the scheduler or other actors.
func (i *inmemory) processorExecuteFn(reminder *api.Reminder) {
	if l := i.actorLoop(reminder.ActorKey()); l != nil {
		l.Enqueue(&eventFire{reminder: reminder})
	}
}

// actorLoop returns the actor's execution loop, creating and starting it on
// first use. It returns nil once the store is closed.
func (i *inmemory) actorLoop(actorKey string) loop.Interface[timerEvent] {
	i.actorLoopsLock.Lock()
	defer i.actorLoopsLock.Unlock()

	if i.closed {
		return nil
	}
	if l, ok := i.actorLoops[actorKey]; ok {
		return l
	}

	l := loopFactory.NewLoop(i)
	i.actorLoops[actorKey] = l
	i.wg.Go(func() {
		defer loopFactory.CacheLoop(l)
		if err := l.Run(i.ctx); err != nil {
			log.Errorf("Actor timer loop for %s stopped with error: %s", actorKey, err)
		}
	})
	return l
}

// Handle runs on an actor's loop goroutine, executing that actor's timer
// callbacks one at a time in submission (scheduled) order.
func (i *inmemory) Handle(ctx context.Context, ev timerEvent) error {
	if fire, ok := ev.(*eventFire); ok {
		i.executeAndReschedule(ctx, fire.reminder)
	}
	return nil
}

// executeAndReschedule invokes a single timer callback and, if the timer
// repeats, re-enqueues its next tick with the scheduler. The next tick is
// enqueued only after the callback returns, so a repeating timer never overlaps
// its own next firing (dapr/dapr#1026).
func (i *inmemory) executeAndReschedule(ctx context.Context, reminder *api.Reminder) {
	err := i.router.CallReminder(ctx, reminder)
	diag.DefaultMonitoring.ActorTimerFired(reminder.ActorType, err == nil)
	if err != nil {
		// Successful and non-successful executions are treated as the same in
		// terms of ticking forward, so we log the error and continue.
		log.Errorf("Error executing timer: %s", err)
	}

	// Advance the schedule on a copy: the scheduler reads ScheduledTime()
	// (RegisteredTime) as its heap key on its own goroutine, so mutating the
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
