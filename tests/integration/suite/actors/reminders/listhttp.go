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

package reminders

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(listhttp))
}

// listhttp asserts that the reminders of a single actor can be listed over
// HTTP, that the list is scoped to the actor, that it agrees with the gRPC
// list, and that it tracks registrations and deletions.
type listhttp struct {
	actors *actors.Actors
}

func (l *listhttp) Setup(t *testing.T) []framework.Option {
	l.actors = actors.New(t,
		actors.WithActorTypes("foo"),
		actors.WithActorTypeHandler("foo", func(http.ResponseWriter, *http.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(l.actors),
	}
}

func (l *listhttp) Run(t *testing.T, ctx context.Context) {
	l.actors.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	gclient := l.actors.Daprd().GRPCClient(t, ctx)

	httpDo := func(t *testing.T, method, url, body string) (int, string) {
		t.Helper()
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, r)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode, strings.TrimSpace(string(b))
	}
	httpList := func(t *testing.T, actorType, actorID string) (int, string) {
		t.Helper()
		return httpDo(t, http.MethodGet, l.actors.Daprd().ActorRemindersURL(actorType, actorID), "")
	}
	register := func(t *testing.T, actorID, name, body string) {
		t.Helper()
		code, _ := httpDo(t, http.MethodPost, l.actors.Daprd().ActorReminderURL("foo", actorID, name), body)
		require.Equal(t, http.StatusNoContent, code)
	}
	assertErrorCode := func(t *testing.T, body, want string) {
		t.Helper()
		var apiErr struct {
			ErrorCode string `json:"errorCode"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &apiErr))
		assert.Equal(t, want, apiErr.ErrorCode)
	}

	// No reminders registered yet.
	code, body := httpList(t, "foo", "abc-1")
	assert.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"reminders":[]}`, body)

	// Registered reminders are listed with the same representation as GET.
	register(t, "abc-1", "r1", `{"dueTime":"1000s","period":"10s","data":"hello"}`)
	register(t, "abc-1", "r0", `{"dueTime":"1000s","data":{"k":[1,2]}}`)

	code, body = httpList(t, "foo", "abc-1")
	assert.Equal(t, http.StatusOK, code)
	var listed struct {
		Reminders []map[string]any `json:"reminders"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &listed))
	require.Len(t, listed.Reminders, 2)
	byName := make(map[string]map[string]any, 2)
	for _, r := range listed.Reminders {
		name, ok := r["name"].(string)
		require.True(t, ok)
		byName[name] = r
	}
	require.Contains(t, byName, "r0")
	require.Contains(t, byName, "r1")
	assert.Equal(t, map[string]any{
		"name": "r1", "actorType": "foo", "actorID": "abc-1",
		"dueTime": "1000s", "period": "@every 10s", "data": "hello",
	}, byName["r1"])
	assert.Equal(t, map[string]any{
		"name": "r0", "actorType": "foo", "actorID": "abc-1",
		"dueTime": "1000s", "data": map[string]any{"k": []any{float64(1), float64(2)}},
	}, byName["r0"])

	// HTTP and gRPC agree.
	gresp, err := gclient.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{ActorType: "foo", ActorId: new("abc-1")})
	require.NoError(t, err)
	assert.Len(t, gresp.GetReminders(), 2)

	// The list is scoped to the exact actor ID, not a prefix.
	code, body = httpList(t, "foo", "abc-")
	assert.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"reminders":[]}`, body)

	register(t, "abc-2", "r1", `{"dueTime":"1000s"}`)
	code, body = httpList(t, "foo", "abc-2")
	assert.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"reminders":[{"name":"r1","actorType":"foo","actorID":"abc-2","dueTime":"1000s"}]}`, body)
	code, body = httpList(t, "foo", "abc-1")
	assert.Equal(t, http.StatusOK, code)
	require.NoError(t, json.Unmarshal([]byte(body), &listed))
	assert.Len(t, listed.Reminders, 2)

	// A non-hosted actor type and a reserved internal type are rejected.
	code, body = httpList(t, "bar", "abc-1")
	assert.Equal(t, http.StatusForbidden, code)
	assertErrorCode(t, body, "ERR_ACTOR_REMINDER_NON_HOSTED")

	code, body = httpList(t, "dapr.internal.default."+l.actors.AppID()+".workflow", "abc-1")
	assert.Equal(t, http.StatusForbidden, code)
	assertErrorCode(t, body, "ERR_ACTOR_TYPE_RESERVED")

	// Deleting a reminder removes it from the list.
	code, _ = httpDo(t, http.MethodDelete, l.actors.Daprd().ActorReminderURL("foo", "abc-1", "r1"), "")
	require.Equal(t, http.StatusNoContent, code)

	code, body = httpList(t, "foo", "abc-1")
	assert.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"reminders":[{"name":"r0","actorType":"foo","actorID":"abc-1","dueTime":"1000s","data":{"k":[1,2]}}]}`, body)
}
