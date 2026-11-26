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

package statestore

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribstate "github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(notransactional))
}

// notransactional ensures that the state store bound by
// spec.workflow.stateStore must support the ETag and transactional features,
// as the default actor state store must. With a bound store without
// transactions, daprd warns at startup, and the workflow operations fail with
// an error naming that store instead of running on the default one.
type notransactional struct {
	workflow *workflow.Workflow
	store    *statestore.StateStore
	logline  *logline.LogLine
}

func (n *notransactional) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	n.logline = logline.New(t, logline.WithStdoutLineContains(
		`.workflow will fail: actors: state store \"wallet-workflows\" bound to actor type`,
	))

	sock := socket.New(t)
	n.store = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(inmemory.New(t, inmemory.WithFeatures(contribstate.FeatureETag))),
	)

	n.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithLogLineStdout(n.logline),
			daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: wallet-workflows
spec:
  type: state.`+n.store.SocketName()+`
  version: v1
`),
			daprd.WithConfigManifests(t, workflowStateStoreConfig("wallet-workflows")),
		),
	)

	return []framework.Option{
		framework.WithProcesses(n.logline, n.store, n.workflow),
	}
}

func (n *notransactional) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	n.workflow.Registry().AddWorkflowN("foo", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})

	n.logline.EventuallyFoundAll(t)

	const notTransactional = `state store "wallet-workflows" bound to actor type "`
	const needsFeatures = `.workflow" must support both the ETag and transactional features required for actor state`
	assertError := func(t *testing.T, err error) {
		t.Helper()
		require.ErrorContains(t, err, notTransactional)
		require.ErrorContains(t, err, needsFeatures)
	}

	client := n.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "foo")
	assertError(t, err)
	assertError(t, client.RaiseEvent(ctx, "foo-instance", "event"))
	assertError(t, client.PurgeWorkflowState(ctx, "foo-instance"))

	_, err = n.workflow.WorkflowClient(t, ctx).ListInstanceIDs(ctx)
	assertError(t, err)

	// The default actor state store holds no workflow state.
	for _, key := range n.workflow.DB().StateKeys(t, ctx) {
		assert.NotContains(t, key, "||dapr.internal.")
	}
}
