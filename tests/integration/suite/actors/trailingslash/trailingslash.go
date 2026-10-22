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

package trailingslash

import (
	"context"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(trailingslash))
}

// trailingslash covers dapr/dapr#9833's note that the Actors API also uses
// NormalizeMethod (pkg/actors/targets/app/transport/http/http.go), so actor
// methods invoked with a trailing slash must be forwarded to the actor app
// with that trailing slash intact, matching the dapr/dapr#7686 fix for
// regular service invocation.
type trailingslash struct {
	caller  *actors.Actors
	callee  *actors.Actors
	lastReq chan string
}

func (t *trailingslash) Setup(tst *testing.T) []framework.Option {
	t.lastReq = make(chan string, 1)

	t.caller = actors.New(tst)
	t.callee = actors.New(tst,
		actors.WithPeerActor(t.caller),
		actors.WithActorTypes("mytype"),
		actors.WithActorTypeHandler("mytype", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			select {
			case t.lastReq <- r.URL.Path:
			default:
			}
			w.WriteHeader(nethttp.StatusOK)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(t.caller, t.callee),
	}
}

func (t *trailingslash) Run(tst *testing.T, ctx context.Context) {
	t.caller.WaitUntilRunning(tst, ctx)
	t.callee.WaitUntilRunning(tst, ctx)

	assert.EventuallyWithT(tst, func(c *assert.CollectT) {
		table := t.caller.Placement().PlacementTables(tst, ctx)
		if !assert.Contains(c, table.Tables, "default") {
			return
		}
		hosts := table.Tables["default"].Hosts
		if !assert.NotEmpty(c, hosts) {
			return
		}
		assert.Contains(c, hosts[0].Entities, "mytype")
	}, time.Second*10, time.Millisecond*10)

	client := t.caller.GRPCClient(tst, ctx)

	invoke := func(tst *testing.T, method string) string {
		tst.Helper()
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "mytype",
			ActorId:   "1",
			Method:    method,
		})
		require.NoError(tst, err)
		select {
		case got := <-t.lastReq:
			return got
		default:
			require.Fail(tst, "actor app was not invoked")
			return ""
		}
	}

	tst.Run("trailing slash preserved to actor app", func(tst *testing.T) {
		got := invoke(tst, "foo/")
		assert.Equal(tst, "/actors/mytype/1/method/foo/", got)
	})

	tst.Run("no trailing slash stays without one", func(tst *testing.T) {
		got := invoke(tst, "foo")
		assert.Equal(tst, "/actors/mytype/1/method/foo", got)
	})

	tst.Run("nested method trailing slash preserved", func(tst *testing.T) {
		got := invoke(tst, "foo/bar/")
		assert.Equal(tst, "/actors/mytype/1/method/foo/bar/", got)
	})
}
