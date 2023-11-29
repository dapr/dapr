/*
Copyright 2023 The Dapr Authors
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
	nethttp "net/http"

	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
	"github.com/dapr/dapr/pkg/http/endpoints"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/go-chi/chi/v5"
)

var endpointGroupActorV1State = &endpoints.EndpointGroup{
	Name:                 endpoints.EndpointGroupActors,
	Version:              endpoints.EndpointGroupVersion1,
	AppendSpanAttributes: appendActorStateSpanAttributesFn,
}

// For timers and reminders
var endpointGroupActorV1Misc = &endpoints.EndpointGroup{
	Name:                 endpoints.EndpointGroupActors,
	Version:              endpoints.EndpointGroupVersion1,
	AppendSpanAttributes: nil, // TODO
}

func appendActorStateSpanAttributesFn(r *nethttp.Request, m map[string]string) {
	m[diagConsts.DaprAPIActorTypeID] = chi.URLParam(r, actorTypeParam) + "." + chi.URLParam(r, actorIDParam)
	m[diagConsts.DBSystemSpanAttributeKey] = diagConsts.StateBuildingBlockType
	m[diagConsts.DBConnectionStringSpanAttributeKey] = diagConsts.StateBuildingBlockType
	m[diagConsts.DBStatementSpanAttributeKey] = r.Method + " " + r.URL.Path
	m[diagConsts.DBNameSpanAttributeKey] = "actor"
}

func appendActorInvocationSpanAttributesFn(r *nethttp.Request, m map[string]string) {
	actorType := chi.URLParam(r, actorTypeParam)
	actorTypeID := actorType + "." + chi.URLParam(r, actorIDParam)
	m[diagConsts.DaprAPIActorTypeID] = actorTypeID
	m[diagConsts.GrpcServiceSpanAttributeKey] = "ServiceInvocation"
	m[diagConsts.NetPeerNameSpanAttributeKey] = actorTypeID
	m[diagConsts.DaprAPISpanNameInternal] = "CallActor/" + actorType + "/" + chi.URLParam(r, "method")
}

func actorInvocationMethodNameFn(r *nethttp.Request) string {
	return "InvokeActor/" + chi.URLParam(r, actorTypeParam) + "." + chi.URLParam(r, actorIDParam)
}

func (a *api) constructActorEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "actors/{actorType}/{actorId}/state",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1State,
			FastHTTPHandler: a.onActorStateTransaction,
			Settings: endpoints.EndpointSettings{
				Name: "ExecuteActorStateTransaction",
			},
		},
		{
			Methods: []string{nethttp.MethodGet, nethttp.MethodPost, nethttp.MethodDelete, nethttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/method/{method}",
			Version: apiVersionV1,
			Group: &endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupActors,
				Version:              endpoints.EndpointGroupVersion1,
				AppendSpanAttributes: appendActorInvocationSpanAttributesFn,
				MethodName:           actorInvocationMethodNameFn,
			},
			FastHTTPHandler: a.onDirectActorMessage,
			Settings: endpoints.EndpointSettings{
				Name: "InvokeActor",
			},
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "actors/{actorType}/{actorId}/state/{key}",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1State,
			FastHTTPHandler: a.onGetActorState,
			Settings: endpoints.EndpointSettings{
				Name: "GetActorState",
			},
		},
		{
			Methods: []string{nethttp.MethodDelete},
			Route:   "actors/{actorType}/{actorId}/state/{key}",
			Version: apiVersionV1,
			Group:   endpointGroupActorV1State,
			Handler: a.onDeleteActorStateHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "DeleteActorState",
			},
		},
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "actors/{actorType}/{actorId}/reminders/{name}",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1Misc,
			FastHTTPHandler: a.onCreateActorReminder,
			Settings: endpoints.EndpointSettings{
				Name: "RegisterActorReminder",
			},
		},
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "actors/{actorType}/{actorId}/timers/{name}",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1Misc,
			FastHTTPHandler: a.onCreateActorTimer,
			Settings: endpoints.EndpointSettings{
				Name: "RegisterActorTimer",
			},
		},
		{
			Methods:         []string{nethttp.MethodDelete},
			Route:           "actors/{actorType}/{actorId}/reminders/{name}",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1Misc,
			FastHTTPHandler: a.onDeleteActorReminder,
			Settings: endpoints.EndpointSettings{
				Name: "UnregisterActorReminder",
			},
		},
		{
			Methods:         []string{nethttp.MethodDelete},
			Route:           "actors/{actorType}/{actorId}/timers/{name}",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1Misc,
			FastHTTPHandler: a.onDeleteActorTimer,
			Settings: endpoints.EndpointSettings{
				Name: "UnregisterActorTimer",
			},
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "actors/{actorType}/{actorId}/reminders/{name}",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1Misc,
			FastHTTPHandler: a.onGetActorReminder,
			Settings: endpoints.EndpointSettings{
				Name: "GetActorReminder",
			},
		},
	}
}

// Route: DELETE "actors/{actorType}/{actorId}/state/{key}"
func (a *api) onDeleteActorStateHandler() nethttp.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.DeleteActorState,
		UniversalHTTPHandlerOpts[*runtimev1pb.DeleteActorStateRequest, *runtimev1pb.DeleteActorStateResponse]{
			InModifier: func(r *nethttp.Request, in *runtimev1pb.DeleteActorStateRequest) (*runtimev1pb.DeleteActorStateRequest, error) {
				in.ActorId = chi.URLParam(r, actorIDParam)
				in.ActorType = chi.URLParam(r, actorTypeParam)
				return in, nil
			},
			SuccessStatusCode: nethttp.StatusNoContent,
		})
}
