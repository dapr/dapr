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

package timers

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(list))
}

// list asserts that the timers registered for an actor can be read back over
// both HTTP and gRPC, that the list is scoped to the actor, and that it tracks
// registrations and deletions.
type list struct {
	app *actors.Actors
}

func (l *list) Setup(t *testing.T) []framework.Option {
	l.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(l.app),
	}
}

func (l *list) Run(t *testing.T, ctx context.Context) {
	l.app.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	gclient := l.app.Daprd().GRPCClient(t, ctx)

	httpList := func(t *testing.T, actorType, actorID string) (int, string) {
		t.Helper()
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, l.app.Daprd().ActorTimersURL(actorType, actorID), nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode, strings.TrimSpace(string(b))
	}

	// No timers registered yet.
	gresp, err := gclient.ListActorTimers(ctx, &rtv1.ListActorTimersRequest{ActorType: "abc", ActorId: "foo"})
	require.NoError(t, err)
	assert.Empty(t, gresp.GetTimers())

	code, body := httpList(t, "abc", "foo")
	assert.Equal(t, nethttp.StatusOK, code)
	assert.JSONEq(t, `{"timers":[]}`, body)

	// Register one timer over HTTP and one over gRPC. Long due times keep them
	// from firing (and from being rescheduled) while the test inspects them.
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, l.app.Daprd().ActorTimerURL("abc", "foo", "t1"),
		strings.NewReader(`{"dueTime":"1000s","period":"10s","ttl":"2552-01-01T00:00:00Z","callback":"cb","data":"hello"}`))
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	_, err = gclient.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "abc", ActorId: "foo", Name: "t0",
		DueTime: "1000s", Data: []byte("hi"),
	})
	require.NoError(t, err)

	// Both timers are listed, sorted by name.
	gresp, err = gclient.ListActorTimers(ctx, &rtv1.ListActorTimersRequest{ActorType: "abc", ActorId: "foo"})
	require.NoError(t, err)
	require.Len(t, gresp.GetTimers(), 2)

	// gRPC data is the JSON encoding of the request bytes, i.e. base64.
	data0, err := anypb.New(wrapperspb.Bytes([]byte(`"aGk="`)))
	require.NoError(t, err)
	data1, err := anypb.New(wrapperspb.Bytes([]byte(`"hello"`)))
	require.NoError(t, err)

	assert.Equal(t, "t0", gresp.GetTimers()[0].GetName())
	assert.True(t, proto.Equal(&rtv1.ActorTimer{
		ActorType: "abc", ActorId: "foo",
		DueTime: new("1000s"),
		Data:    data0,
	}, gresp.GetTimers()[0].GetTimer()), gresp.GetTimers()[0].GetTimer().String())

	assert.Equal(t, "t1", gresp.GetTimers()[1].GetName())
	assert.True(t, proto.Equal(&rtv1.ActorTimer{
		ActorType: "abc", ActorId: "foo",
		DueTime:  new("1000s"),
		Period:   new("10s"),
		Ttl:      new("2552-01-01T00:00:00Z"),
		Callback: new("cb"),
		Data:     data1,
	}, gresp.GetTimers()[1].GetTimer()), gresp.GetTimers()[1].GetTimer().String())

	code, body = httpList(t, "abc", "foo")
	assert.Equal(t, nethttp.StatusOK, code)
	assert.JSONEq(t, `{"timers":[
		{"name":"t0","actorType":"abc","actorID":"foo","dueTime":"1000s","data":"aGk="},
		{"name":"t1","actorType":"abc","actorID":"foo","dueTime":"1000s","period":"10s","ttl":"2552-01-01T00:00:00Z","callback":"cb","data":"hello"}
	]}`, body)

	// The list is scoped to the actor instance.
	gresp, err = gclient.ListActorTimers(ctx, &rtv1.ListActorTimersRequest{ActorType: "abc", ActorId: "other"})
	require.NoError(t, err)
	assert.Empty(t, gresp.GetTimers())

	// A non-hosted actor type is rejected.
	_, err = gclient.ListActorTimers(ctx, &rtv1.ListActorTimersRequest{ActorType: "xyz", ActorId: "foo"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())

	code, body = httpList(t, "xyz", "foo")
	assert.Equal(t, nethttp.StatusForbidden, code)
	var apiErr struct {
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &apiErr))
	assert.Equal(t, "ERR_ACTOR_TIMER_NON_HOSTED", apiErr.ErrorCode)

	// Deleting a timer removes it from the list.
	_, err = gclient.UnregisterActorTimer(ctx, &rtv1.UnregisterActorTimerRequest{ActorType: "abc", ActorId: "foo", Name: "t1"})
	require.NoError(t, err)

	gresp, err = gclient.ListActorTimers(ctx, &rtv1.ListActorTimersRequest{ActorType: "abc", ActorId: "foo"})
	require.NoError(t, err)
	require.Len(t, gresp.GetTimers(), 1)
	assert.Equal(t, "t0", gresp.GetTimers()[0].GetName())

	code, body = httpList(t, "abc", "foo")
	assert.Equal(t, nethttp.StatusOK, code)
	assert.JSONEq(t, `{"timers":[{"name":"t0","actorType":"abc","actorID":"foo","dueTime":"1000s","data":"aGk="}]}`, body)
}
