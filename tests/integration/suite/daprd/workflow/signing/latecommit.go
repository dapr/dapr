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

package signing

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(latecommit))
}

const (
	lateCommitDropLog = "dropping completion with no matching scheduled task in signed history"

	// Common to the drop above and to the fixed path's "has no scheduled
	// task in signed history yet; asking the sender to retry".
	lateCommitRefusedLog = "scheduled task in signed history"
)

// latecommit reproduces an activity completion that is verified against
// durable history before the turn that scheduled it is readable there. The
// turn dispatches step1 before it saves; the store acknowledges that save but
// serves reads from before it for a while (a store whose reads lag its
// writes, or a commit still landing when the actor changes host), and the
// etag refresh after the save fails so the actor drops its cache. step1's
// completion then finds no TaskScheduled in a fresh load. Under history
// signing it used to be dropped as unmatched, its sender acked, and the
// instance stranded RUNNING once the row became visible. It must be refused
// recoverably so the sender retries with the result in hand. The shared
// latecommitCase is also run with the WorkflowsFastPath off by
// latecommitreminder.
type latecommit struct {
	latecommitCase
}

type latecommitCase struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
	logline  *logline.LogLine
}

func (l *latecommit) Setup(t *testing.T) []framework.Option {
	return l.setup(t, true)
}

func (l *latecommitCase) setup(t *testing.T, fastPath bool) []framework.Option {
	os.SkipWindows(t)

	l.store = fault.New(t)
	sock := socket.New(t)
	l.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(l.store),
	)
	component := fmt.Sprintf(`
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
`, l.ss.SocketName())
	l.logline = logline.New(t, logline.WithCaptureAll())

	l.workflow = workflow.New(t,
		workflow.WithHistorySigning(t),
		workflow.WithFastPath(fastPath),
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithResourceFiles(component),
			daprd.WithExecOptions(exec.WithStdout(l.logline.Stdout()), exec.WithStderr(l.logline.Stderr())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(l.logline, l.ss, l.workflow),
	}
}

func (l *latecommit) Run(t *testing.T, ctx context.Context) {
	l.run(t, ctx)
}

func (l *latecommitCase) run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	const id = "latecommit"

	var step1Runs atomic.Int64
	reg := l.workflow.Registry()
	require.NoError(t, reg.AddActivityN("step0", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, reg.AddActivityN("step1", func(task.ActivityContext) (any, error) {
		step1Runs.Add(1)
		return "late", nil
	}))
	require.NoError(t, reg.AddWorkflowN("latecommit", func(wctx *task.WorkflowContext) (any, error) {
		if err := wctx.CallActivity("step0").Await(nil); err != nil {
			return nil, err
		}
		var out string
		if err := wctx.CallActivity("step1").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	// The turn that records step1's TaskScheduled is acknowledged but not
	// readable until applied, and the etag refresh right after it fails so
	// the actor reloads from the store on its next operation.
	var (
		armOnce sync.Once
		apply   func() error
		arrived <-chan struct{}
	)
	armed := make(chan struct{})
	l.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			set, ok := op.(state.SetRequest)
			if !ok || !strings.Contains(set.Key, "||history-") {
				continue
			}
			if value, ok := set.Value.([]byte); ok && bytes.Contains(value, []byte("step1")) {
				armOnce.Do(func() {
					arrived, apply = l.store.ArmMultiWriteBehind("||history-")
					l.store.ArmGetFailures("||"+id+"||metadata", 1, nil)
					close(armed)
				})
				return
			}
		}
	})
	t.Cleanup(func() {
		if apply != nil {
			_ = apply()
		}
	})

	client := l.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "latecommit", api.WithInstanceID(id))
	require.NoError(t, err)

	select {
	case <-armed:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the turn scheduling step1 must save")
	}
	select {
	case <-arrived:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the save recording step1's TaskScheduled must be captured")
	}

	// step1 completes at once and its completion is verified against a
	// fresh load that does not show its scheduling yet. Let the write land
	// only after that verdict.
	require.Eventually(t, func() bool {
		return l.logline.Contains(lateCommitRefusedLog)
	}, time.Second*20, time.Millisecond*10, "the completion must reach the actor before its scheduling is readable")
	require.NoError(t, apply())

	waitCtx, cancel := context.WithTimeout(ctx, time.Second*20)
	defer cancel()
	meta, err := client.WaitForWorkflowCompletion(waitCtx, id)
	require.NoError(t, err, "the instance must complete once its scheduling row is readable")
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), "%v", meta.GetFailureDetails())
	assert.JSONEq(t, `"late"`, meta.GetOutput().GetValue())
	assert.False(t, l.logline.Contains(lateCommitDropLog),
		"a completion whose scheduling is not readable yet must be retried, not dropped")
	assert.Equal(t, int64(1), step1Runs.Load(),
		"the contract is at-least-once; the retried completion must not need a second execution")
}
