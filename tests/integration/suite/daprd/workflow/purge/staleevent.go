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

package purge

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(staleevent))
}

// staleevent purges a completed instance whose state delete lands but whose
// reminder delete fails, then delivers a late activity result and an
// external event to the actor before its asynchronous deactivation runs. The
// purge must drop the cached state whatever else failed, or the events are
// admitted against the pre-purge cache and written back over the deleted
// rows: a metadata row declaring the purged history's length with no history
// rows, which every later load rejects.
type staleevent struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
	sched    *scheduler.Scheduler
	proxy    *proxy.Proxy
}

func (s *staleevent) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	appID := uuid.New().String()
	sen := sentry.New(t)
	s.sched = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithID("dapr-scheduler-server-0"),
	)
	s.proxy = proxy.New(t, s.sched, proxy.WithSentry(t, sen, "default", appID))

	s.store = fault.New(t)
	sock := socket.New(t)
	s.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(s.store),
	)

	s.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithSentryInstance(sen),
		workflow.WithSchedulerInstance(s.sched),
		workflow.WithSchedulerAddress(s.proxy.Address()),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID(appID),
			daprd.WithSocket(t, sock),
			daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
  metadata:
  - name: actorStateStore
    value: "true"
`, s.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(sen, s.sched, s.proxy, s.ss, s.workflow),
	}
}

func (s *staleevent) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const id = api.InstanceID("purge-staleevent")
	release := make(chan struct{})
	// Idempotent and registered up front: a failed assertion must not leave
	// the activity blocked through teardown.
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	var held atomic.Bool
	reg := s.workflow.Registry()
	require.NoError(t, reg.AddActivityN("late", func(task.ActivityContext) (any, error) {
		if held.CompareAndSwap(false, true) {
			<-release
		}
		return nil, nil
	}))
	require.NoError(t, reg.AddWorkflowN("staleevent", func(wctx *task.WorkflowContext) (any, error) {
		wctx.CallActivity("late")
		return "done", nil
	}))

	// Record every Multi against this instance after the purge's delete,
	// up to the fresh create's first save (an empty history): a stale
	// write declares the purged history's length with no history rows.
	metadataKey := "||" + string(id) + "||metadata"
	var purged, recreated atomic.Bool
	var written atomic.Int64
	var lastWrite atomic.Pointer[string]
	s.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			switch r := op.(type) {
			case state.DeleteRequest:
				if strings.HasSuffix(r.Key, metadataKey) {
					purged.Store(true)
				}
			case state.SetRequest:
				if !purged.Load() || recreated.Load() || !strings.HasSuffix(r.Key, metadataKey) {
					continue
				}
				var meta backend.BackendWorkflowStateMetadata
				if b, ok := r.Value.([]byte); ok && proto.Unmarshal(b, &meta) == nil && meta.GetHistoryLength() == 0 {
					recreated.Store(true)
					continue
				}
				keys := make([]string, 0, len(req.Operations))
				for _, o := range req.Operations {
					keys = append(keys, string(o.Operation())+" "+o.GetKey())
				}
				desc := fmt.Sprintf("historyLength=%d: %s", meta.GetHistoryLength(), strings.Join(keys, ", "))
				lastWrite.Store(&desc)
				written.Add(1)
			}
		}
	})

	client := s.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "staleevent", api.WithInstanceID(id))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	require.Eventually(t, held.Load, time.Second*10, time.Millisecond*10, "the late activity must be running")

	// The purge's state delete lands, its reminder deletes fail. The delete
	// is held while the late result is published and the event raised, so
	// both queue on the actor lock ahead of the deactivation the purge
	// queues.
	s.proxy.ArmFailures(proxy.MethodDeleteByMetadata, 3, codes.Unavailable, nil)
	arrived, releaseDelete, _ := s.store.ArmMultiDeleteHold(metadataKey)
	t.Cleanup(releaseDelete)
	purgeErr := make(chan error, 1)
	go func() { purgeErr <- client.PurgeWorkflowState(ctx, id) }()
	select {
	case <-arrived:
	case <-time.After(time.Second * 10):
		require.Fail(t, "the purge commit was never attempted")
	}
	releaseOnce()
	raiseErr := make(chan error, 1)
	go func() { raiseErr <- client.RaiseEvent(ctx, id, "late") }()
	time.Sleep(time.Millisecond * 500)
	releaseDelete()
	<-purgeErr
	<-raiseErr
	require.GreaterOrEqual(t, s.proxy.FailedCount(), 1, "the reminder delete must have failed")

	// Queued behind the late result: when this create returns the result
	// has been judged, and the instance must start fresh.
	_, err = client.ScheduleNewWorkflow(ctx, "staleevent", api.WithInstanceID(id))
	require.NoError(t, err, "scheduling the same ID after the purge must succeed")
	last := ""
	if p := lastWrite.Load(); p != nil {
		last = *p
	}
	assert.Zero(t, written.Load(), "nothing may write the purged instance's state back from memory: "+last)
	meta, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	ns, appID := s.workflow.Dapr().Namespace(), s.workflow.Dapr().AppID()
	zero := func(n int) bool { return n == 0 }
	s.sched.WaitJobKeyCount(t, ctx, fmt.Sprintf("||dapr.internal.%s.%s.workflow||%s||", ns, appID, id), zero)
	s.sched.WaitJobKeyCount(t, ctx, fmt.Sprintf("||dapr.internal.%s.%s.activity||%s::", ns, appID, id), zero)
}
