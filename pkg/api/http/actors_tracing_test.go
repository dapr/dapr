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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
)

// TestAppendActorReminderTimerSpanAttributesFn verifies that the reminder and
// timer endpoints emit a bounded span name that keeps only the (bounded)
// actorType, dropping the unbounded actorId and reminder/timer name. Using the
// raw request path produced one span name per actor instance per reminder,
// blowing up tracing cardinality.
//
// Regression test for https://github.com/dapr/dapr/issues/4703
func TestAppendActorReminderTimerSpanAttributesFn(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		path         string
		endpointName string
		wantSpan     string
	}{
		{"create reminder", http.MethodPost, "/v1.0/actors/OrderActor/order-123/reminders/CancelOrder", "RegisterActorReminder", "RegisterActorReminder/OrderActor"},
		{"delete reminder", http.MethodDelete, "/v1.0/actors/OrderActor/order-123/reminders/CancelOrder", "UnregisterActorReminder", "UnregisterActorReminder/OrderActor"},
		{"get reminder", http.MethodGet, "/v1.0/actors/OrderActor/order-123/reminders/CancelOrder", "GetActorReminder", "GetActorReminder/OrderActor"},
		{"create timer", http.MethodPost, "/v1.0/actors/OrderActor/order-123/timers/Refresh", "RegisterActorTimer", "RegisterActorTimer/OrderActor"},
		{"delete timer", http.MethodDelete, "/v1.0/actors/OrderActor/order-123/timers/Refresh", "UnregisterActorTimer", "UnregisterActorTimer/OrderActor"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chiCtx := chi.NewRouteContext()
			chiCtx.URLParams.Add(actorTypeParam, "OrderActor")
			chiCtx.URLParams.Add(actorIDParam, "order-123")
			chiCtx.URLParams.Add(nameParam, "CancelOrder")

			req := httptest.NewRequest(tc.method, tc.path, nil)
			ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
			ctx = context.WithValue(ctx, endpoints.EndpointCtxKey{}, &endpoints.EndpointCtxData{
				Settings: endpoints.EndpointSettings{Name: tc.endpointName},
			})
			req = req.WithContext(ctx)

			m := map[string]string{}
			appendActorReminderTimerSpanAttributesFn(req, m)

			span := m[diagConsts.DaprAPISpanNameInternal]
			assert.Equal(t, tc.wantSpan, span)
			assert.NotContains(t, span, "order-123", "span name must not contain unbounded actorId")
			assert.NotContains(t, span, "CancelOrder", "span name must not contain unbounded reminder/timer name")
			assert.Equal(t, "OrderActor.order-123", m[diagConsts.DaprAPIActorTypeID])
		})
	}
}
