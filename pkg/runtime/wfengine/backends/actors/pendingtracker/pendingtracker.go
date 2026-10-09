/*
Copyright 2026 The Dapr Authors
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

package pendingtracker

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/logger"
)

const cancelTimeout = 10 * time.Second

var log = logger.NewLogger("dapr.wfengine.backend.actors.pendingtracker")

// Backend is the pending-tasks surface the tracker wraps. It is satisfied by
// both the local and the cluster tasks backends.
type Backend interface {
	CancelActivityTask(ctx context.Context, instanceID api.InstanceID, taskID int32) error
	CancelWorkflowTask(ctx context.Context, instanceID api.InstanceID) error
	CompleteActivityTask(ctx context.Context, response *protos.ActivityResponse) error
	CompleteWorkflowTask(ctx context.Context, response *protos.WorkflowResponse) error
	OnActivityCompletion(request *protos.ActivityRequest, cb func(*protos.ActivityResponse, error)) func()
	OnWorkflowTaskCompletion(request *protos.WorkflowRequest, cb func(*protos.WorkflowResponse, error)) func()
}

// Tracker decorates a Backend with executor-connectivity-aware cancellation
// of pending completions.
type Tracker struct {
	Backend

	// available is the executor connectivity flag. While false, every
	// pending registration is cancelled: at the flip via the sweep, and on
	// arrival for registrations racing the flip.
	available atomic.Bool

	// Registrations pending per key. Several executions of the same task can
	// be pending at once, and each must stay tracked until it deregisters.
	lock       sync.Mutex
	workflows  map[string]int
	activities map[string]*activityKey
}

type activityKey struct {
	instanceID string
	taskID     int32
	pending    int
}

func New(inner Backend) *Tracker {
	t := &Tracker{
		Backend:    inner,
		workflows:  make(map[string]int),
		activities: make(map[string]*activityKey),
	}
	// Available until an executor-count transition says otherwise: work items
	// cannot be dispatched before the first executor registers actors, and
	// defaulting to unavailable would cancel registrations made by callers
	// that never report connectivity.
	t.available.Store(true)
	return t
}

// SetExecutorAvailable flips executor connectivity. Flipping to false sweeps
// and cancels every currently pending task: with no executor connected,
// nothing can ever complete them. The sweep returns only once every cancel
// has settled (cancelled, completed under the race, or timed out), so the
// caller's subsequent unregister HaltAll does not wait on a turn whose
// cancellation is still in flight.
func (t *Tracker) SetExecutorAvailable(available bool) {
	t.available.Store(available)
	if available {
		return
	}

	t.lock.Lock()
	workflows := make([]string, 0, len(t.workflows))
	for iid := range t.workflows {
		workflows = append(workflows, iid)
	}
	activities := make([]*activityKey, 0, len(t.activities))
	for _, ak := range t.activities {
		activities = append(activities, ak)
	}
	t.lock.Unlock()

	var wg sync.WaitGroup
	for _, iid := range workflows {
		wg.Go(func() {
			t.cancelWorkflow(iid)
		})
	}
	for _, ak := range activities {
		wg.Go(func() {
			t.cancelActivity(ak)
		})
	}
	wg.Wait()
}

// OnWorkflowTaskCompletion implements Backend. Registrations made while no
// executor is available are cancelled immediately: they can never be
// completed, and leaving them parked recreates the unregister deadlock.
func (t *Tracker) OnWorkflowTaskCompletion(req *protos.WorkflowRequest, cb func(*protos.WorkflowResponse, error)) func() {
	iid := req.GetInstanceId()
	dereg := t.Backend.OnWorkflowTaskCompletion(req, cb)

	t.lock.Lock()
	t.workflows[iid]++
	t.lock.Unlock()

	if !t.available.Load() {
		t.cancelWorkflow(iid)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			t.lock.Lock()
			if t.workflows[iid]--; t.workflows[iid] == 0 {
				delete(t.workflows, iid)
			}
			t.lock.Unlock()
		})
		dereg()
	}
}

// OnActivityCompletion implements Backend; see OnWorkflowTaskCompletion.
func (t *Tracker) OnActivityCompletion(req *protos.ActivityRequest, cb func(*protos.ActivityResponse, error)) func() {
	iid, taskID := req.GetWorkflowInstance().GetInstanceId(), req.GetTaskId()
	key := backend.GetActivityExecutionKey(iid, taskID)
	dereg := t.Backend.OnActivityCompletion(req, cb)

	t.lock.Lock()
	ak, ok := t.activities[key]
	if !ok {
		ak = &activityKey{instanceID: iid, taskID: taskID}
		t.activities[key] = ak
	}
	ak.pending++
	t.lock.Unlock()

	if !t.available.Load() {
		t.cancelActivity(ak)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			t.lock.Lock()
			if ak.pending--; ak.pending == 0 && t.activities[key] == ak {
				delete(t.activities, key)
			}
			t.lock.Unlock()
		})
		dereg()
	}
}

func (t *Tracker) cancelWorkflow(instanceID string) {
	t.cancelWithRetry(
		func(ctx context.Context) error {
			return t.CancelWorkflowTask(ctx, api.InstanceID(instanceID))
		},
		func() bool {
			t.lock.Lock()
			defer t.lock.Unlock()
			_, ok := t.workflows[instanceID]
			return ok
		},
		"workflow task for instance '"+instanceID+"'",
	)
}

func (t *Tracker) cancelActivity(ak *activityKey) {
	key := backend.GetActivityExecutionKey(ak.instanceID, ak.taskID)
	t.cancelWithRetry(
		func(ctx context.Context) error {
			return t.CancelActivityTask(ctx, api.InstanceID(ak.instanceID), ak.taskID)
		},
		func() bool {
			t.lock.Lock()
			defer t.lock.Unlock()
			_, ok := t.activities[key]
			return ok
		},
		"activity task '"+key+"'",
	)
}

// cancelWithRetry drives one cancellation to a settled outcome within
// cancelTimeout. An error with the registration already gone is the benign
// completion race (the executor's delivery arbiter accepted another
// settlement) and is dropped. An error with the registration still live can
// be a transient failure of the cluster backend's non-local fall-through (a
// watch-path waiter is cancelled via a remote executor-actor call), and is
// retried: giving up would leave the turn parked and recreate the unregister
// deadlock this package exists to break. An executor reconnecting mid-retry
// stops the retry, since the completion can then be delivered normally.
func (t *Tracker) cancelWithRetry(cancelTask func(context.Context) error, registered func() bool, desc string) {
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()

	backoff := 50 * time.Millisecond
	for {
		err := cancelTask(ctx)
		if err == nil {
			return
		}
		if !registered() {
			log.Debugf("No pending %s to cancel: %v", desc, err)
			return
		}
		if t.available.Load() {
			log.Debugf("An executor reconnected while cancelling the pending %s; leaving it to complete: %v", desc, err)
			return
		}
		select {
		case <-ctx.Done():
			log.Warnf("Failed to cancel the pending %s within %s; its completion stays parked until an executor reconnects: %v",
				desc, cancelTimeout, err)
			return
		case <-time.After(backoff):
			backoff = min(backoff*2, time.Second)
		}
	}
}
