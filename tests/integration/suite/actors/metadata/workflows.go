/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metadata

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workflows))
}

type workflows struct {
	workflow *workflow.Workflow
}

func (w *workflows) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype", "myothertype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})

	srv := prochttp.New(t, prochttp.WithHandler(handler))

	w.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithAppPort(srv.Port())),
	)

	return []framework.Option{
		framework.WithProcesses(srv, w.workflow),
	}
}

func (w *workflows) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	hold := make(chan struct{})
	w.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		id1 := ctx.CallActivity("bar")
		id2 := ctx.CallActivity("bar")
		return nil, errors.Join(id1.Await(nil), id2.Await(nil))
	})
	w.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		<-hold
		return nil, nil
	})

	client := w.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewOrchestration(ctx, "foo", api.WithInstanceID("my-id-1"))
	require.NoError(t, err)
	_, err = client.ScheduleNewOrchestration(ctx, "foo", api.WithInstanceID("my-id-2"))
	require.NoError(t, err)

	t.Cleanup(func() { close(hold) })

	gclient := w.workflow.Dapr().GRPCClient(t, ctx)
	_, err = gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "myactortype",
		ActorId:   "1",
		Method:    "my-method",
	})
	require.NoError(t, err)
	_, err = gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "myactortype",
		ActorId:   "2",
		Method:    "my-method",
	})
	require.NoError(t, err)

	_, err = gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "myothertype",
		ActorId:   "1",
		Method:    "my-method",
	})
	require.NoError(t, err)

	assert.ElementsMatch(t, []*daprd.MetadataActorRuntimeActiveActor{
		{
			Type:  "dapr.internal.default." + w.workflow.Dapr().AppID() + ".workflow",
			Count: 2,
		},
		{
			Type:  "dapr.internal.default." + w.workflow.Dapr().AppID() + ".activity",
			Count: 4,
		},
		{
			Type:  "dapr.internal.default." + w.workflow.Dapr().AppID() + ".retentioner",
			Count: 0,
		},
		{
			Type:  "myactortype",
			Count: 2,
		},
		{
			Type:  "myothertype",
			Count: 1,
		},
	}, w.workflow.Dapr().GetMetaActorRuntime(t, ctx).ActiveActors)
}
