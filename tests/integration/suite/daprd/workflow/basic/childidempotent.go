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

package basic

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	suite.Register(new(childidempotent))
}

// childidempotent verifies duplicate child creation is treated idempotently
// when a parent crashes after creating the child but before saving its own state.
type childidempotent struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *failOnceMultiStore
}

const (
	parentID = "parent-crash-test"
	childID  = "parent-crash-test:child"
)

func (c *childidempotent) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	c.store = &failOnceMultiStore{Wrapped: inmemory.New(t).(*inmemory.Wrapped)}

	sock := socket.New(t)
	c.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(c.store),
	)

	c.workflow = workflow.New(t,
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
`, c.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.ss, c.workflow),
	}
}

func (c *childidempotent) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	gclient := c.workflow.GRPCClient(t, ctx)

	blockedCh := make(chan struct{})
	failedCh := make(chan struct{})

	r := c.workflow.Registry()

	require.NoError(t, r.AddActivityN("blocking", func(actx task.ActivityContext) (any, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-blockedCh:
			return "activity done", nil
		}
	}))

	require.NoError(t, r.AddOrchestratorN("child", func(octx *task.OrchestrationContext) (any, error) {
		var result string
		if err := octx.CallActivity("blocking").Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	}))

	require.NoError(t, r.AddOrchestratorN("parent", func(octx *task.OrchestrationContext) (any, error) {
		var output string
		err := octx.CallSubOrchestrator("child",
			task.WithSubOrchestrationInstanceID(childID),
		).Await(&output)
		return output, err
	}))

	c.workflow.BackendClient(t, ctx)

	c.store.ArmFailureForKey(parentID+"||history-", failedCh)

	// Uses StartWorkflowBeta1 instead of ScheduleNewOrchestration because it returns immediately
	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "parent",
		InstanceId:        parentID,
	})
	require.NoError(t, err)

	select {
	case <-failedCh:
	case <-time.After(15 * time.Second):
		require.Fail(t, "injected save failure did not fire")
	}

	savedCh := make(chan struct{})
	c.store.WatchForSuccessfulSave(parentID+"||history-", savedCh)

	startReminderPrefix := fmt.Sprintf(
		"dapr/jobs/actorreminder||default||dapr.internal.default.%s.workflow||%s||start-",
		c.workflow.Dapr().AppID(),
		parentID,
	)
	assert.EventuallyWithT(t, func(ac *assert.CollectT) {
		keys := c.workflow.Scheduler().ListAllKeys(t, ctx, startReminderPrefix)
		assert.Empty(ac, keys)
	}, 15*time.Second, 10*time.Millisecond)

	_, err = gclient.RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
		InstanceId:        parentID,
		WorkflowComponent: "dapr",
		EventName:         "wakeup",
	})
	require.NoError(t, err)

	select {
	case <-savedCh:
	case <-time.After(15 * time.Second):
		require.Fail(t, "parent did not save state after re-execution (fix may not be active)")
	}

	close(blockedCh)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        parentID,
			WorkflowComponent: "dapr",
		})
		if assert.NoError(c, err) {
			assert.Equal(c, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 30*time.Second, 10*time.Millisecond)

	assert.True(t, c.store.DidFail(), "injected save failure should have triggered")
}

type failOnceMultiStore struct {
	*inmemory.Wrapped

	mu sync.Mutex

	failKeySubstring string
	failNotifyCh     chan struct{}
	didFail          atomic.Bool

	watchKeySubstring string
	watchNotifyCh     chan struct{}
}

func (s *failOnceMultiStore) Multi(ctx context.Context, req *state.TransactionalStateRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// In-memory state store mutates req.Operations, so capture keys first.
	keys := make([]string, len(req.Operations))
	for i, op := range req.Operations {
		switch v := op.(type) {
		case state.SetRequest:
			keys[i] = v.Key
		case state.DeleteRequest:
			keys[i] = v.Key
		}
	}

	if s.failKeySubstring != "" && containsAny(keys, s.failKeySubstring) {
		s.didFail.Store(true)
		if s.failNotifyCh != nil {
			close(s.failNotifyCh)
		}
		s.failKeySubstring = ""
		s.failNotifyCh = nil
		return errors.New("injected transient failure for testing")
	}

	err := s.Wrapped.Store.(state.TransactionalStore).Multi(ctx, req)
	if err == nil {
		if s.watchKeySubstring != "" && containsAny(keys, s.watchKeySubstring) {
			if s.watchNotifyCh != nil {
				close(s.watchNotifyCh)
			}
			s.watchKeySubstring = ""
			s.watchNotifyCh = nil
		}
	}

	return err
}

func (s *failOnceMultiStore) MultiMaxSize() int {
	return -1
}

func (s *failOnceMultiStore) ArmFailureForKey(substring string, failedCh chan struct{}) {
	s.mu.Lock()
	s.failNotifyCh = failedCh
	s.failKeySubstring = substring
	s.mu.Unlock()
}

func (s *failOnceMultiStore) WatchForSuccessfulSave(substring string, savedCh chan struct{}) {
	s.mu.Lock()
	s.watchNotifyCh = savedCh
	s.watchKeySubstring = substring
	s.mu.Unlock()
}

func (s *failOnceMultiStore) DidFail() bool {
	return s.didFail.Load()
}

func containsAny(keys []string, substr string) bool {
	for _, key := range keys {
		if strings.Contains(key, substr) {
			return true
		}
	}
	return false
}
