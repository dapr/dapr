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

package http

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cenkalti/backoff/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/channel/fake"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

func newHTTPTransport(invoke func(context.Context, *invokev1.InvokeMethodRequest, string) (*invokev1.InvokeMethodResponse, error)) *Transport {
	ch := fake.New().WithInvokeMethod(invoke)
	return New(ch, resiliency.New(logger.NewLogger("test")), "myactortype")
}

func TestInvoke_UsesActorPathAndPUT(t *testing.T) {
	var seenMethod string
	var seenVerb string
	tp := newHTTPTransport(func(_ context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
		seenMethod = req.Message().GetMethod()
		seenVerb = req.Message().GetHttpExtension().GetVerb().String()
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	})

	req := internalv1pb.NewInternalInvokeRequest("doSomething").
		WithActor("myactortype", "actor-1")

	_, err := tp.Invoke(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, "actors/myactortype/actor-1/method/doSomething", seenMethod)
	assert.Equal(t, "PUT", seenVerb)
}

func TestInvoke_NotFoundIsPermanent(t *testing.T) {
	tp := newHTTPTransport(func(context.Context, *invokev1.InvokeMethodRequest, string) (*invokev1.InvokeMethodResponse, error) {
		return invokev1.NewInvokeMethodResponse(http.StatusNotFound, "", nil), nil
	})

	req := internalv1pb.NewInternalInvokeRequest("missing").
		WithActor("myactortype", "a")

	_, err := tp.Invoke(t.Context(), req)
	require.Error(t, err)

	var perm *backoff.PermanentError
	assert.ErrorAs(t, err, &perm, "404 must be wrapped in backoff.Permanent")
}

func TestInvoke_ActorErrorHeader(t *testing.T) {
	tp := newHTTPTransport(func(context.Context, *invokev1.InvokeMethodRequest, string) (*invokev1.InvokeMethodResponse, error) {
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil).
			WithHTTPHeaders(map[string][]string{"X-Daprerrorresponseheader": {"true"}}), nil
	})

	req := internalv1pb.NewInternalInvokeRequest("m").WithActor("myactortype", "a")
	_, err := tp.Invoke(t.Context(), req)
	require.Error(t, err)
	assert.True(t, actorerrors.Is(err))
}

func TestInvoke_ReminderCancelHeader(t *testing.T) {
	tp := newHTTPTransport(func(context.Context, *invokev1.InvokeMethodRequest, string) (*invokev1.InvokeMethodResponse, error) {
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil).
			WithHTTPHeaders(map[string][]string{"X-Daprremindercancel": {"true"}}), nil
	})

	req := internalv1pb.NewInternalInvokeRequest("m").WithActor("myactortype", "a")
	_, err := tp.Invoke(t.Context(), req)
	require.Error(t, err)
	assert.ErrorIs(t, err, actorerrors.ErrReminderCanceled)
}

func TestInvokeReminder_SerializesJSONBody(t *testing.T) {
	var seenPath string
	var seenBody map[string]any
	tp := newHTTPTransport(func(_ context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
		seenPath = req.Message().GetMethod()
		raw, _ := req.RawDataFull()
		_ = json.Unmarshal(raw, &seenBody)
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	})

	err := tp.InvokeReminder(t.Context(), &api.Reminder{
		ActorType: "myactortype",
		ActorID:   "a",
		Name:      "daily",
		DueTime:   "1h",
	})
	require.NoError(t, err)
	assert.Equal(t, "actors/myactortype/a/method/remind/daily", seenPath)
	assert.Equal(t, "1h", seenBody["dueTime"])
}

func TestInvokeTimer_SerializesCallback(t *testing.T) {
	var seenBody map[string]any
	tp := newHTTPTransport(func(_ context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
		raw, _ := req.RawDataFull()
		_ = json.Unmarshal(raw, &seenBody)
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	})

	err := tp.InvokeTimer(t.Context(), &api.Reminder{
		ActorType: "myactortype",
		ActorID:   "a",
		Name:      "tick",
		Callback:  "cb",
	})
	require.NoError(t, err)
	assert.Equal(t, "cb", seenBody["callback"])
}

func TestDeactivate_UsesDELETE(t *testing.T) {
	var seenPath string
	var seenVerb string
	tp := newHTTPTransport(func(_ context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
		seenPath = req.Message().GetMethod()
		seenVerb = req.Message().GetHttpExtension().GetVerb().String()
		return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
	})

	err := tp.Deactivate(t.Context(), "myactortype", "a")
	require.NoError(t, err)
	assert.Equal(t, "actors/myactortype/a", seenPath)
	assert.Equal(t, "DELETE", seenVerb)
}

func TestDeactivate_NonOKFails(t *testing.T) {
	tp := newHTTPTransport(func(context.Context, *invokev1.InvokeMethodRequest, string) (*invokev1.InvokeMethodResponse, error) {
		return invokev1.NewInvokeMethodResponse(http.StatusInternalServerError, "boom", nil), nil
	})

	err := tp.Deactivate(t.Context(), "myactortype", "a")
	require.Error(t, err)
}
