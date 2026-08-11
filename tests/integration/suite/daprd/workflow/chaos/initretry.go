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

package chaos

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(initretry))
}

type initretry struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *initFailStore
}

type initFailStore struct {
	*inmemory.WrappedTransactionalMultiMaxSize

	lock     sync.Mutex
	allow    bool
	attempts atomic.Int32
}

func (s *initFailStore) Init(ctx context.Context, md state.Metadata) error {
	s.attempts.Add(1)
	s.lock.Lock()
	allow := s.allow
	s.lock.Unlock()
	if !allow {
		return errors.New("connection refused: state store down")
	}
	return s.WrappedTransactionalMultiMaxSize.Init(ctx, md)
}

func (s *initFailStore) release() {
	s.lock.Lock()
	s.allow = true
	s.lock.Unlock()
}

func (i *initretry) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	i.store = &initFailStore{
		WrappedTransactionalMultiMaxSize: inmemory.NewTransactionalMultiMaxSize(t).(*inmemory.WrappedTransactionalMultiMaxSize),
	}

	sock := socket.New(t)
	i.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(i.store),
	)

	i.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0,
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
`, i.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(i.ss, i.workflow),
	}
}

func (i *initretry) Run(t *testing.T, ctx context.Context) {
	assert.Eventually(t, func() bool {
		return i.store.attempts.Load() >= 2
	}, 30*time.Second, 10*time.Millisecond)

	i.store.release()
	i.workflow.WaitUntilRunning(t, ctx)

	r := i.workflow.Registry()
	require.NoError(t, r.AddActivityN("act", func(task.ActivityContext) (any, error) {
		return "ok", nil
	}))
	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		var out string
		if err := octx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	i.workflow.BackendClient(t, ctx)

	const wfID = "initretry-wf"
	gclient := i.workflow.GRPCClient(t, ctx)
	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        wfID,
			WorkflowComponent: "dapr",
		})
		if assert.NoError(co, gerr) {
			assert.Equal(co, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 30*time.Second, 10*time.Millisecond)

	assert.GreaterOrEqual(t, i.store.attempts.Load(), int32(3))
}
