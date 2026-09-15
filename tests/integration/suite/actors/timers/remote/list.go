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

package remote

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(list))
}

// list asserts that timers are host-local: only the sidecar that owns the
// actor can list them, any other sidecar is rejected with
// ERR_ACTOR_TIMER_NOT_OWNED.
type list struct {
	app1 *actors.Actors
	app2 *actors.Actors
}

func (l *list) Setup(t *testing.T) []framework.Option {
	l.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {}),
	)
	l.app2 = actors.New(t,
		actors.WithPeerActor(l.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(l.app1, l.app2),
	}
}

func (l *list) Run(t *testing.T, ctx context.Context) {
	l.app1.WaitUntilRunning(t, ctx)
	l.app2.WaitUntilRunning(t, ctx)

	client1 := l.app1.Daprd().GRPCClient(t, ctx)
	client2 := l.app2.Daprd().GRPCClient(t, ctx)
	httpClient := client.HTTP(t)

	register := func(t require.TestingT, c rtv1.DaprClient, id string) codes.Code {
		_, err := c.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
			ActorType: "abc", ActorId: id, Name: "foo", DueTime: "1000s",
		})
		return status.Code(err)
	}

	// Wait until both hosts agree on the placement table before requiring
	// exactly one owner per actor.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		code1 := register(c, client1, "probe")
		code2 := register(c, client2, "probe")
		assert.ElementsMatch(c, []codes.Code{codes.OK, codes.PermissionDenied}, []codes.Code{code1, code2})
	}, time.Second*10, time.Millisecond*10)

	// Find an actor owned by each host, registering one timer on each.
	owner1, owner2 := "", ""
	for i := 0; owner1 == "" || owner2 == ""; i++ {
		require.Less(t, i, 100, "actor IDs never hashed to both hosts")
		id := strconv.Itoa(i)
		code1 := register(t, client1, id)
		code2 := register(t, client2, id)
		assert.ElementsMatch(t, []codes.Code{codes.OK, codes.PermissionDenied}, []codes.Code{code1, code2})
		if code1 == codes.OK && owner1 == "" {
			owner1 = id
		}
		if code2 == codes.OK && owner2 == "" {
			owner2 = id
		}
	}

	httpList := func(t *testing.T, app *actors.Actors, id string) (int, string) {
		t.Helper()
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, app.Daprd().ActorTimersURL("abc", id), nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode, string(b)
	}

	httpGet := func(t *testing.T, app *actors.Actors, id string) (int, string) {
		t.Helper()
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, app.Daprd().ActorTimerURL("abc", id, "foo"), nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode, string(b)
	}

	assertNotOwnedBody := func(t *testing.T, code int, body string) {
		t.Helper()
		assert.Equal(t, nethttp.StatusForbidden, code)
		var apiErr struct {
			ErrorCode string `json:"errorCode"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &apiErr))
		assert.Equal(t, "ERR_ACTOR_TIMER_NOT_OWNED", apiErr.ErrorCode)
	}

	assertNotOwned := func(t *testing.T, c rtv1.DaprClient, app *actors.Actors, id string) {
		t.Helper()
		_, err := c.ListActorTimers(ctx, &rtv1.ListActorTimersRequest{ActorType: "abc", ActorId: id})
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
		_, err = c.GetActorTimer(ctx, &rtv1.GetActorTimerRequest{ActorType: "abc", ActorId: id, Name: "foo"})
		assert.Equal(t, codes.PermissionDenied, status.Code(err))

		code, body := httpList(t, app, id)
		assertNotOwnedBody(t, code, body)
		code, body = httpGet(t, app, id)
		assertNotOwnedBody(t, code, body)
	}

	assertOwned := func(t *testing.T, c rtv1.DaprClient, app *actors.Actors, id string) {
		t.Helper()
		resp, err := c.ListActorTimers(ctx, &rtv1.ListActorTimersRequest{ActorType: "abc", ActorId: id})
		require.NoError(t, err)
		require.Len(t, resp.GetTimers(), 1)
		assert.Equal(t, "foo", resp.GetTimers()[0].GetName())
		assert.Equal(t, id, resp.GetTimers()[0].GetTimer().GetActorId())

		code, body := httpList(t, app, id)
		assert.Equal(t, nethttp.StatusOK, code)
		assert.JSONEq(t, `{"timers":[{"name":"foo","actorType":"abc","actorID":"`+id+`","dueTime":"1000s"}]}`, body)

		gresp, err := c.GetActorTimer(ctx, &rtv1.GetActorTimerRequest{ActorType: "abc", ActorId: id, Name: "foo"})
		require.NoError(t, err)
		assert.Equal(t, id, gresp.GetActorId())
		assert.Equal(t, "1000s", gresp.GetDueTime())

		code, body = httpGet(t, app, id)
		assert.Equal(t, nethttp.StatusOK, code)
		assert.JSONEq(t, `{"actorType":"abc","actorID":"`+id+`","dueTime":"1000s"}`, body)
	}

	// The non-owning host is rejected, the owning host lists the timer.
	assertNotOwned(t, client2, l.app2, owner1)
	assertNotOwned(t, client1, l.app1, owner2)
	assertOwned(t, client1, l.app1, owner1)
	assertOwned(t, client2, l.app2, owner2)
}
