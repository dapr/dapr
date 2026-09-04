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

	workflows  sync.Map // instanceID string -> token *byte
	activities sync.Map // execution key string -> *activityKey
}

type activityKey struct {
	instanceID string
	taskID     int32
}

func New(inner Backend) *Tracker {
	t := &Tracker{Backend: inner}
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

	var wg sync.WaitGroup
	t.workflows.Range(func(key, _ any) bool {
		wg.Go(func() {
			t.cancelWorkflow(key.(string))
		})
		return true
	})
	t.activities.Range(func(_, value any) bool {
		wg.Go(func() {
			t.cancelActivity(value.(*activityKey))
		})
		return true
	})
	wg.Wait()
}

// OnWorkflowTaskCompletion implements Backend. Registrations made while no
// executor is available are cancelled immediately: they can never be
// completed, and leaving them parked recreates the unregister deadlock.
func (t *Tracker) OnWorkflowTaskCompletion(req *protos.WorkflowRequest, cb func(*protos.WorkflowResponse, error)) func() {
	iid := req.GetInstanceId()
	dereg := t.Backend.OnWorkflowTaskCompletion(req, cb)

	// Unique token so a superseded attempt's late deregister cannot evict a
	// newer attempt's tracking entry for the same instance. Non-zero-size:
	// zero-size allocations share an address, which would defeat the compare.
	token := new(byte)
	t.workflows.Store(iid, token)

	if !t.available.Load() {
		t.cancelWorkflow(iid)
	}

	return func() {
		t.workflows.CompareAndDelete(iid, token)
		dereg()
	}
}

// OnActivityCompletion implements Backend; see OnWorkflowTaskCompletion.
func (t *Tracker) OnActivityCompletion(req *protos.ActivityRequest, cb func(*protos.ActivityResponse, error)) func() {
	ak := &activityKey{
		instanceID: req.GetWorkflowInstance().GetInstanceId(),
		taskID:     req.GetTaskId(),
	}
	key := backend.GetActivityExecutionKey(ak.instanceID, ak.taskID)
	dereg := t.Backend.OnActivityCompletion(req, cb)

	t.activities.Store(key, ak)

	if !t.available.Load() {
		t.cancelActivity(ak)
	}

	return func() {
		t.activities.CompareAndDelete(key, ak)
		dereg()
	}
}

func (t *Tracker) cancelWorkflow(instanceID string) {
	t.cancelWithRetry(
		func(ctx context.Context) error {
			return t.CancelWorkflowTask(ctx, api.InstanceID(instanceID))
		},
		func() bool {
			_, ok := t.workflows.Load(instanceID)
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
			_, ok := t.activities.Load(key)
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
