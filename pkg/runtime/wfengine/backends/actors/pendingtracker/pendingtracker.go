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
// nothing can ever complete them.
func (t *Tracker) SetExecutorAvailable(available bool) {
	t.available.Store(available)
	if available {
		return
	}

	t.workflows.Range(func(key, _ any) bool {
		t.cancelWorkflow(key.(string))
		return true
	})
	t.activities.Range(func(_, value any) bool {
		t.cancelActivity(value.(*activityKey))
		return true
	})
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

// cancelWorkflow cancels one pending workflow task. Errors are expected when
// a completion or an earlier cancel raced the sweep and are safe to drop: the
// executor's delivery arbiter accepts a single settlement per dispatch.
func (t *Tracker) cancelWorkflow(instanceID string) {
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()
	if err := t.CancelWorkflowTask(ctx, api.InstanceID(instanceID)); err != nil {
		log.Debugf("No pending workflow task to cancel for instance '%s': %v", instanceID, err)
	}
}

func (t *Tracker) cancelActivity(ak *activityKey) {
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()
	if err := t.CancelActivityTask(ctx, api.InstanceID(ak.instanceID), ak.taskID); err != nil {
		log.Debugf("No pending activity task to cancel for instance '%s' task %d: %v", ak.instanceID, ak.taskID, err)
	}
}
