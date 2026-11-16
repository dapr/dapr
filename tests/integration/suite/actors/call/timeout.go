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

package call

import (
	"context"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(timeout))
}

// timeout runs an actor method past its resiliency timeout on the hosting
// daprd. A gRPC InvokeActor must answer DeadlineExceeded, not Internal, both
// on the hosting daprd and through a peer that calls it remotely.
type timeout struct {
	actors1 *actors.Actors
	actors2 *actors.Actors
	release chan struct{}
}

func (o *timeout) Setup(t *testing.T) []framework.Option {
	o.release = make(chan struct{})
	o.actors1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			select {
			case <-r.Context().Done():
			case <-o.release:
			}
		}),
		actors.WithResources(`apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: resiliency
spec:
  policies:
    timeouts:
      short: 500ms
  targets:
    actors:
      abc:
        timeout: short
`),
	)
	o.actors2 = actors.New(t,
		actors.WithPeerActor(o.actors1),
	)

	return []framework.Option{
		framework.WithProcesses(o.actors1, o.actors2),
	}
}

func (o *timeout) Run(t *testing.T, ctx context.Context) {
	t.Cleanup(func() { close(o.release) })

	o.actors1.WaitUntilRunning(t, ctx)
	o.actors2.WaitUntilRunning(t, ctx)

	for name, actor := range map[string]*actors.Actors{"local": o.actors1, "remote": o.actors2} {
		cctx, cancel := context.WithTimeout(ctx, time.Second*10)
		start := time.Now()
		_, err := actor.GRPCClient(t, ctx).InvokeActor(cctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   name,
			Method:    "foo",
		})
		cancel()
		require.Error(t, err, name)
		assert.Equal(t, codes.DeadlineExceeded, status.Code(err), "%s: %v", name, err)
		assert.Less(t, time.Since(start), time.Second*5, "%s: the resiliency timeout must end the call, not the client deadline", name)
	}
}
