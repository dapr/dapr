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

package rebalance

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
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
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(list))
}

// list asserts what the timer list and get APIs report across a
// dissemination event: timers are host-local, so once an actor moves to
// another host the old host rejects reads with ERR_ACTOR_TIMER_NOT_OWNED, the
// new host has no timer for it until one is registered there, and timers
// registered on a host die with it when the actor moves back.
type list struct {
	app1        *actors.Actors
	deactivated slice.Slice[string]
	fired1      slice.Slice[string]
}

func (l *list) Setup(t *testing.T) []framework.Option {
	l.deactivated = slice.String()
	l.fired1 = slice.String()

	l.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				l.deactivated.Append(r.PathValue("id"))
			}
		}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			l.fired1.Append(r.PathValue("id"))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(l.app1),
	}
}

type apiErr struct {
	ErrorCode string `json:"errorCode"`
}

func (l *list) Run(t *testing.T, ctx context.Context) {
	l.app1.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	body := `{"dueTime":"0s","period":"1s","data":"hello"}`

	httpDo := func(t require.TestingT, method, url string, reqBody string) (int, string) {
		var r io.Reader
		if reqBody != "" {
			r = strings.NewReader(reqBody)
		}
		req, err := nethttp.NewRequestWithContext(ctx, method, url, r)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode, string(b)
	}
	httpList := func(t require.TestingT, app *actors.Actors, id string) (int, string) {
		return httpDo(t, nethttp.MethodGet, app.Daprd().ActorTimersURL("abc", id), "")
	}
	httpGet := func(t require.TestingT, app *actors.Actors, id string) (int, string) {
		return httpDo(t, nethttp.MethodGet, app.Daprd().ActorTimerURL("abc", id, "foo"), "")
	}
	grpcList := func(c rtv1.DaprClient, id string) (*rtv1.ListActorTimersResponse, codes.Code) {
		resp, err := c.ListActorTimers(ctx, &rtv1.ListActorTimersRequest{ActorType: "abc", ActorId: id})
		return resp, status.Code(err)
	}
	grpcGet := func(c rtv1.DaprClient, id string) (*rtv1.GetActorTimerResponse, codes.Code) {
		resp, err := c.GetActorTimer(ctx, &rtv1.GetActorTimerRequest{ActorType: "abc", ActorId: id, Name: "foo"})
		return resp, status.Code(err)
	}

	assertErrorCode := func(t *testing.T, body, want string) {
		t.Helper()
		var e apiErr
		require.NoError(t, json.Unmarshal([]byte(body), &e))
		assert.Equal(t, want, e.ErrorCode)
	}

	// assertHasTimer asserts that app reports the single timer "foo" for id.
	assertHasTimer := func(t *testing.T, app *actors.Actors, c rtv1.DaprClient, id string) {
		t.Helper()
		code, resBody := httpList(t, app, id)
		assert.Equal(t, nethttp.StatusOK, code)
		assert.JSONEq(t, `{"timers":[{"name":"foo","actorType":"abc","actorID":"`+id+`","dueTime":"0s","period":"@every 1s","data":"hello"}]}`, resBody)
		code, resBody = httpGet(t, app, id)
		assert.Equal(t, nethttp.StatusOK, code)
		assert.JSONEq(t, `{"actorType":"abc","actorID":"`+id+`","dueTime":"0s","period":"@every 1s","data":"hello"}`, resBody)

		lresp, gcode := grpcList(c, id)
		require.Equal(t, codes.OK, gcode)
		require.Len(t, lresp.GetTimers(), 1)
		assert.Equal(t, "foo", lresp.GetTimers()[0].GetName())
		gresp, gcode := grpcGet(c, id)
		require.Equal(t, codes.OK, gcode)
		assert.Equal(t, id, gresp.GetActorId())
	}

	// assertNoTimer asserts that app owns id but has no timer for it.
	assertNoTimer := func(t *testing.T, app *actors.Actors, c rtv1.DaprClient, id string) {
		t.Helper()
		code, resBody := httpList(t, app, id)
		assert.Equal(t, nethttp.StatusOK, code)
		assert.JSONEq(t, `{"timers":[]}`, resBody)
		code, resBody = httpGet(t, app, id)
		assert.Equal(t, nethttp.StatusNotFound, code)
		assertErrorCode(t, resBody, "ERR_ACTOR_TIMER_NOT_FOUND")

		lresp, gcode := grpcList(c, id)
		require.Equal(t, codes.OK, gcode)
		assert.Empty(t, lresp.GetTimers())
		_, gcode = grpcGet(c, id)
		assert.Equal(t, codes.NotFound, gcode)
	}

	// assertNotOwned asserts that app rejects reads for id as not owned.
	assertNotOwned := func(t *testing.T, app *actors.Actors, c rtv1.DaprClient, id string) {
		t.Helper()
		code, resBody := httpList(t, app, id)
		assert.Equal(t, nethttp.StatusForbidden, code)
		assertErrorCode(t, resBody, "ERR_ACTOR_TIMER_NOT_OWNED")
		code, resBody = httpGet(t, app, id)
		assert.Equal(t, nethttp.StatusForbidden, code)
		assertErrorCode(t, resBody, "ERR_ACTOR_TIMER_NOT_OWNED")

		_, gcode := grpcList(c, id)
		assert.Equal(t, codes.PermissionDenied, gcode)
		_, gcode = grpcGet(c, id)
		assert.Equal(t, codes.PermissionDenied, gcode)
	}

	client1 := l.app1.Daprd().GRPCClient(t, ctx)

	for i := range 100 {
		code, _ := httpDo(t, nethttp.MethodPost, l.app1.Daprd().ActorTimerURL("abc", strconv.Itoa(i), "foo"), body)
		require.Equal(t, nethttp.StatusNoContent, code)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, l.fired1.Len())
	}, time.Second*10, time.Millisecond*10)

	// Before dissemination the single host owns everything.
	for _, id := range []string{"0", "42", "99"} {
		assertHasTimer(t, l.app1, client1, id)
	}

	fired2 := slice.String()
	app2 := actors.New(t,
		actors.WithPeerActor(l.app1),
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			fired2.Append(r.PathValue("id"))
		}),
	)
	t.Cleanup(func() { app2.Cleanup(t) })
	app2.Run(t, ctx)
	app2.WaitUntilRunning(t, ctx)
	client2 := app2.Daprd().GRPCClient(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		deactivated := l.deactivated.Len()
		assert.Positive(c, deactivated)
		gauge := l.app1.Daprd().Metrics(c, ctx).SumWithLabels("dapr_runtime_actor_timers", "actor_type:abc")
		assert.InDelta(c, float64(100-deactivated), gauge, 0)
	}, time.Second*20, time.Millisecond*10)

	moved := l.deactivated.Slice()[0]

	// The old host no longer owns the actor, and the timer did not follow it
	// to the new host.
	assertNotOwned(t, l.app1, client1, moved)
	assertNoTimer(t, app2, client2, moved)

	// An actor that stayed on the old host is unaffected, and the new host
	// rejects reads for it.
	stayed := ""
	for i := range 100 {
		id := strconv.Itoa(i)
		if code, _ := httpList(t, l.app1, id); code == nethttp.StatusOK {
			stayed = id
			break
		}
	}
	require.NotEmpty(t, stayed, "no actor stayed on the first host")
	assertHasTimer(t, l.app1, client1, stayed)
	assertNotOwned(t, app2, client2, stayed)

	// Registering again on the new host makes the timer visible there only.
	code, _ := httpDo(t, nethttp.MethodPost, app2.Daprd().ActorTimerURL("abc", moved, "foo"), body)
	require.Equal(t, nethttp.StatusNoContent, code)
	assertHasTimer(t, app2, client2, moved)
	assertNotOwned(t, l.app1, client1, moved)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Contains(c, fired2.Slice(), moved)
	}, time.Second*10, time.Millisecond*10)

	// Stopping the new host moves the actor back. The timer registered on the
	// stopped host died with it, so the original host owns the actor again but
	// has nothing registered for it.
	app2.Cleanup(t)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		code, _ := httpList(c, l.app1, moved)
		assert.Equal(c, nethttp.StatusOK, code)
	}, time.Second*20, time.Millisecond*10)
	assertNoTimer(t, l.app1, client1, moved)
	assertHasTimer(t, l.app1, client1, stayed)
}
