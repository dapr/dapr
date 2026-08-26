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

package period

import (
	"context"
	nethttp "net/http"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(complete))
}

type complete struct {
	actors    *actors.Actors
	triggered slice.Slice[string]
}

func (c *complete) Setup(t *testing.T) []framework.Option {
	c.triggered = slice.String()

	c.actors = actors.New(t,
		actors.WithActorTypes("helloworld"),
		actors.WithActorTypeHandler("helloworld", func(w nethttp.ResponseWriter, req *nethttp.Request) {
			if req.Method == nethttp.MethodDelete {
				return
			}
			c.triggered.Append(path.Base(req.URL.Path))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(c.actors),
	}
}

func (c *complete) Run(t *testing.T, ctx context.Context) {
	c.actors.WaitUntilRunning(t, ctx)

	_, err := c.actors.GRPCClient(t, ctx).RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "helloworld",
		ActorId:   "1234",
		Name:      "test",
		DueTime:   "0s",
		Period:    "R3/PT1S",
	})
	require.NoError(t, err)

	exp := []string{"test", "test", "test"}

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.ElementsMatch(col, exp, c.triggered.Slice())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.ElementsMatch(t, exp, c.triggered.Slice())
}
