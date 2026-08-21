/*
Copyright 2024 The Dapr Authors
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

package workflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

type Workflow struct {
	registry  *task.TaskRegistry
	actors    *actors.Actors
	clustered bool
}

func New(t *testing.T, fopts ...Option) *Workflow {
	t.Helper()

	opts := options{
		registry: task.NewTaskRegistry(),
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	clustered := procworkflow.ClusteredDeploymentFromEnv()
	if opts.clustered != nil {
		clustered = *opts.clustered
	}

	// A single manifest carries all harness-driven features: the last config
	// file specifying spec.features replaces earlier feature lists, so
	// separate manifests would not compose.
	features := make([]string, 0, 1)
	if clustered {
		features = append(features, "WorkflowsClusteredDeployment")
	}
	var aopts []actors.Option
	if len(features) > 0 {
		aopts = append(aopts, actors.WithDaprdOptions(
			daprd.WithFeatureEnabled(t, features...),
		))
	}

	return &Workflow{
		registry:  opts.registry,
		actors:    actors.New(t, aopts...),
		clustered: clustered,
	}
}

// ClusteredDeployment reports whether the underlying daprd runs with the
// WorkflowsClusteredDeployment feature flag enabled.
func (w *Workflow) ClusteredDeployment() bool {
	return w.clustered
}

func (w *Workflow) Run(t *testing.T, ctx context.Context) {
	w.actors.Run(t, ctx)
}

func (w *Workflow) Cleanup(t *testing.T) {
	w.actors.Cleanup(t)
}

func (w *Workflow) Registry() *task.TaskRegistry {
	return w.registry
}

func (w *Workflow) BackendClient(t *testing.T, ctx context.Context) *client.TaskHubGrpcClient {
	t.Helper()
	backendClient := client.NewTaskHubGrpcClient(w.actors.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, backendClient.StartWorkItemListener(ctx, w.registry))
	return backendClient
}

func (w *Workflow) GRPCClient(t *testing.T, ctx context.Context) rtv1.DaprClient {
	t.Helper()
	return w.actors.GRPCClient(t, ctx)
}

func (w *Workflow) Metrics(t *testing.T, ctx context.Context) map[string]float64 {
	t.Helper()
	return w.actors.Metrics(t, ctx)
}
