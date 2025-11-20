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

package reminders

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(overwrite))
}

type overwrite struct {
	actors *actors.Actors
}

func (o *overwrite) Setup(t *testing.T) []framework.Option {
	o.actors = actors.New(t,
		actors.WithActorTypes("abc", "foo"),
	)

	return []framework.Option{
		framework.WithProcesses(o.actors),
	}
}

func (o *overwrite) Run(t *testing.T, ctx context.Context) {
	o.actors.WaitUntilRunning(t, ctx)

	t.Run("grpc", func(t *testing.T) {
		client := o.actors.GRPCClient(t, ctx)
		_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "abc", ActorId: "123", Name: "reminder1",
			DueTime: "24h",
		})
		require.NoError(t, err)

		resp, err := client.GetActorReminder(ctx, &rtv1.GetActorReminderRequest{
			ActorType: "abc", ActorId: "123", Name: "reminder1",
		})
		require.NoError(t, err)
		assert.Equal(t, "24h", resp.GetDueTime())

		_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "abc", ActorId: "123", Name: "reminder1",
			DueTime: "48h",
		})
		require.NoError(t, err)

		resp, err = client.GetActorReminder(ctx, &rtv1.GetActorReminderRequest{
			ActorType: "abc", ActorId: "123", Name: "reminder1",
		})
		require.NoError(t, err)
		assert.Equal(t, "48h", resp.GetDueTime())

		_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "abc", ActorId: "123", Name: "reminder1",
			DueTime:   "12h",
			Overwrite: ptr.Of(true),
		})
		require.NoError(t, err)

		resp, err = client.GetActorReminder(ctx, &rtv1.GetActorReminderRequest{
			ActorType: "abc", ActorId: "123", Name: "reminder1",
		})
		require.NoError(t, err)
		assert.Equal(t, "12h", resp.GetDueTime())

		_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "abc", ActorId: "123", Name: "reminder1",
			DueTime:   "48h",
			Overwrite: ptr.Of(false),
		})
		require.Error(t, err)
		status, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, status.Code())
		assert.Equal(t, "actor reminder already exists: reminder1", status.Message())
	})

	t.Run("http", func(t *testing.T) {
		client := client.HTTP(t)

		url := o.actors.Daprd().ActorReminderURL("foo", "1234", "helloworld")

		body := `{"data":"reminderdata","dueTime":"12h"}`
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := client.Do(req)
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
		assert.JSONEq(t, `{"actorID":"1234", "actorType":"foo", "data":"reminderdata", "dueTime":"12h"}`, strings.TrimSpace(string(b)))

		body = `{"data":"reminderdata","dueTime":"48h"}`
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
		b, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.JSONEq(t, `{"actorID":"1234", "actorType":"foo", "data":"reminderdata", "dueTime":"48h"}`, strings.TrimSpace(string(b)))

		body = `{"data":"reminderdata","dueTime":"12h", "overwrite": true}`
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
		b, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.JSONEq(t, `{"actorID":"1234", "actorType":"foo", "data":"reminderdata", "dueTime":"12h"}`, strings.TrimSpace(string(b)))

		body = `{"data":"reminderdata","dueTime":"48h", "overwrite": false}`
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		resp, err = client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusConflict, resp.StatusCode)
		b, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.JSONEq(t, `{"errorCode":"ERR_ACTOR_REMINDER_ALREADY_EXISTS","message":"actor reminder already exists: helloworld"}`, strings.TrimSpace(string(b)))
	})
}
