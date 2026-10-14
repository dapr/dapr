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

package restart

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(restart))
}

// restart kills daprd while the app keeps running. Every call in one
// activation carries the same activation id, and the first call after the
// restart carries a new one.
type restart struct {
	app *actors.Actors

	rec activation.Recorder
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	r.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}/method/{method}", func(_ http.ResponseWriter, req *http.Request) {
			r.rec.Record("invoke", activation.FromHeader(req))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(r.app),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.app.WaitUntilRunning(t, ctx)

	r.invoke(t, ctx)
	r.invoke(t, ctx)
	before := r.rec.Take()
	require.GreaterOrEqual(t, len(before), 2)
	require.NotEmpty(t, before[0].ID)
	for _, e := range before {
		assert.Equal(t, before[0].ID, e.ID)
	}

	r.app.Daprd().Restart(t, ctx)
	r.app.Daprd().WaitUntilRunning(t, ctx)
	r.app.Daprd().WaitUntilActorTypeHosted(t, ctx, "abc")

	r.invoke(t, ctx)
	r.invoke(t, ctx)
	after := r.rec.Take()
	require.GreaterOrEqual(t, len(after), 2)
	require.NotEmpty(t, after[0].ID)
	for _, e := range after {
		assert.Equal(t, after[0].ID, e.ID)
	}
	assert.NotEqual(t, before[0].ID, after[0].ID)
}

func (r *restart) invoke(t *testing.T, ctx context.Context) {
	t.Helper()
	client := r.app.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "1",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*8, time.Millisecond*10)
}
