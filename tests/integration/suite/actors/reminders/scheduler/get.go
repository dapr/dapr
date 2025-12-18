/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or impliei.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scheduler

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(get))
}

type get struct {
	actors *actors.Actors
}

func (g *get) Setup(t *testing.T) []framework.Option {
	g.actors = actors.New(t,
		actors.WithActorTypes("foo"),
		actors.WithActorTypeHandler("foo", func(http.ResponseWriter, *http.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(g.actors),
	}
}

func (g *get) Run(t *testing.T, ctx context.Context) {
	g.actors.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	gclient := g.actors.Daprd().GRPCClient(t, ctx)

	url := g.actors.Daprd().ActorReminderURL("foo", "1234", "helloworld")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	gresp, err := gclient.GetActorReminder(ctx, &rtv1.GetActorReminderRequest{
		ActorType: "foo",
		ActorId:   "1234",
		Name:      "helloworld",
	})
	require.Error(t, err)
	status, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, status.Code())
	assert.Equal(t, "actor reminder not found: helloworld", status.Message())
	assert.Nil(t, gresp)

	body := `{"data":"reminderdata","dueTime":"1s","period":"1s","ttl":"2552-01-01T00:00:00Z"}`
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.JSONEq(t, `{"period":"@every 1s","data":"reminderdata","actorID":"1234","actorType":"foo","dueTime":"1s","ttl":"2552-01-01T00:00:00Z" }`, strings.TrimSpace(string(b)))

	gresp, err = gclient.GetActorReminder(ctx, &rtv1.GetActorReminderRequest{
		ActorType: "foo",
		ActorId:   "1234",
		Name:      "helloworld",
	})
	require.NoError(t, err)
	data, err := anypb.New(wrapperspb.Bytes([]byte(`"reminderdata"`)))
	require.NoError(t, err)
	exp := &rtv1.GetActorReminderResponse{
		ActorType: "foo",
		ActorId:   "1234",
		Data:      data,
		DueTime:   ptr.Of("1s"),
		Period:    ptr.Of("@every 1s"),
		Ttl:       ptr.Of(time.Date(2552, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)),
	}
	assert.True(t, proto.Equal(exp, gresp), "%v != %v", exp, gresp)
}
