/*
Copyright 2025 The Dapr Authors
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

package nostate

import (
	"context"
	"fmt"
	nethttp "net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noActorState))
}

type noActorState struct {
	clientApp       *actors.Actors
	hostApp         *actors.Actors
	invocationCount atomic.Int64
	clientLogs      *logline.LogLine
}

func (p *noActorState) Setup(t *testing.T) []framework.Option {
	p.invocationCount.Store(0)

	// Create actor host app meaning with statestore & actor types
	p.hostApp = actors.New(t,
		actors.WithActorTypes("testActor"),
		actors.WithActorTypeHandler("testActor", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			p.invocationCount.Add(1)
		}),
		actors.WithActorStateStore(true),
	)

	p.clientLogs = logline.New(t, logline.WithStdoutLineContains(
		"Actor state store not configured",
		"Connected to placement",
	))

	// Create actor client app meaning no state store & no actor types
	p.clientApp = actors.New(t,
		actors.WithActorStateStore(false),
		actors.WithDaprdOptions(
			daprd.WithExecOptions(
				exec.WithStdout(p.clientLogs.Stdout()),
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(
			p.hostApp,
			p.clientLogs,
			p.clientApp,
		),
	}
}

func (p *noActorState) Run(t *testing.T, ctx context.Context) {
	p.hostApp.WaitUntilRunning(t, ctx)
	p.clientApp.WaitUntilRunning(t, ctx)

	p.clientLogs.EventuallyFoundAll(t)

	// test invocation from client to host for both gRPC & HTTP
	gclient := p.clientApp.Daprd().GRPCClient(t, ctx)
	_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "testActor",
		ActorId:   "test1",
		Method:    "test",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), p.invocationCount.Load())

	hclient := client.HTTP(t)
	url := fmt.Sprintf("http://%s/v1.0/actors/testActor/test1/method/test", p.clientApp.Daprd().HTTPAddress())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err := hclient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, nethttp.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(2), p.invocationCount.Load())
}
