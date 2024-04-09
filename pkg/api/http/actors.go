/*
Copyright 2024 The Dapr Authors
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
	"errors"
	"net/http"
	"strings"

	"github.com/dapr/dapr/pkg/actors"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/api/http/endpoints"
	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/go-chi/chi/v5"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc/codes"
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

func appendActorStateSpanAttributesFn(r *http.Request, m map[string]string) {
	m[diagConsts.DaprAPIActorTypeID] = chi.URLParam(r, actorTypeParam) + "." + chi.URLParam(r, actorIDParam)
	m[diagConsts.DBSystemSpanAttributeKey] = diagConsts.StateBuildingBlockType
	m[diagConsts.DBConnectionStringSpanAttributeKey] = diagConsts.StateBuildingBlockType
	m[diagConsts.DBStatementSpanAttributeKey] = r.Method + " " + r.URL.Path
	m[diagConsts.DBNameSpanAttributeKey] = "actor"
}

func appendActorInvocationSpanAttributesFn(r *http.Request, m map[string]string) {
	actorType := chi.URLParam(r, actorTypeParam)
	actorTypeID := actorType + "." + chi.URLParam(r, actorIDParam)
	m[diagConsts.DaprAPIActorTypeID] = actorTypeID
	m[diagConsts.GrpcServiceSpanAttributeKey] = "ServiceInvocation"
	m[diagConsts.NetPeerNameSpanAttributeKey] = actorTypeID
	m[diagConsts.DaprAPISpanNameInternal] = "CallActor/" + actorType + "/" + chi.URLParam(r, "method")
}

func actorInvocationMethodNameFn(r *http.Request) string {
	return "InvokeActor/" + chi.URLParam(r, actorTypeParam) + "." + chi.URLParam(r, actorIDParam)
}

func (a *api) constructActorEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods:         []string{http.MethodPost, http.MethodPut},
			Route:           "actors/{actorType}/{actorId}/state",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1State,
			FastHTTPHandler: a.onActorStateTransaction,
			Settings: endpoints.EndpointSettings{
				Name: "ExecuteActorStateTransaction",
			},
		},
		{
			Methods: []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodPut},
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
			Methods:         []string{http.MethodGet},
			Route:           "actors/{actorType}/{actorId}/state/{key}",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1State,
			FastHTTPHandler: a.onGetActorState,
			Settings: endpoints.EndpointSettings{
				Name: "GetActorState",
			},
		},
		{
			Methods:         []string{http.MethodPost, http.MethodPut},
			Route:           "actors/{actorType}/{actorId}/reminders/{name}",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1Misc,
			FastHTTPHandler: a.onCreateActorReminder,
			Settings: endpoints.EndpointSettings{
				Name: "RegisterActorReminder",
			},
		},
		{
			Methods:         []string{http.MethodPost, http.MethodPut},
			Route:           "actors/{actorType}/{actorId}/timers/{name}",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1Misc,
			FastHTTPHandler: a.onCreateActorTimer,
			Settings: endpoints.EndpointSettings{
				Name: "RegisterActorTimer",
			},
		},
		{
			Methods:         []string{http.MethodDelete},
			Route:           "actors/{actorType}/{actorId}/reminders/{name}",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1Misc,
			FastHTTPHandler: a.onDeleteActorReminder,
			Settings: endpoints.EndpointSettings{
				Name: "UnregisterActorReminder",
			},
		},
		{
			Methods:         []string{http.MethodDelete},
			Route:           "actors/{actorType}/{actorId}/timers/{name}",
			Version:         apiVersionV1,
			Group:           endpointGroupActorV1Misc,
			FastHTTPHandler: a.onDeleteActorTimer,
			Settings: endpoints.EndpointSettings{
				Name: "UnregisterActorTimer",
			},
		},
		{
			Methods:         []string{http.MethodGet},
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

func (a *api) onCreateActorReminder(reqCtx *fasthttp.RequestCtx) {
	if !a.actorReadinessCheckFastHTTP(reqCtx) {
		return
	}
	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	var req actors.CreateReminderRequest
	err := json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		universalFastHTTPErrorResponder(reqCtx, msg)
		log.Debug(msg)
		return
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.universal.Actors().CreateReminder(reqCtx, &req)
	if err != nil {
		if errors.Is(err, actors.ErrReminderOpActorNotHosted) {
			msg := messages.ErrActorReminderOpActorNotHosted
			universalFastHTTPErrorResponder(reqCtx, msg)
			log.Debug(msg)
			return
		}

		msg := messages.ErrActorReminderCreate.WithFormat(err)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(http.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
}

func (a *api) onCreateActorTimer(reqCtx *fasthttp.RequestCtx) {
	if !a.actorReadinessCheckFastHTTP(reqCtx) {
		// Response already sent
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	var req actors.CreateTimerRequest
	err := json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		universalFastHTTPErrorResponder(reqCtx, msg)
		log.Debug(msg)
		return
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.universal.Actors().CreateTimer(reqCtx, &req)
	if err != nil {
		msg := messages.ErrActorTimerCreate.WithFormat(err)
		universalFastHTTPErrorResponder(reqCtx, msg)
		log.Debug(msg)
		return
	}

	fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
}

func (a *api) onDeleteActorReminder(reqCtx *fasthttp.RequestCtx) {
	if !a.actorReadinessCheckFastHTTP(reqCtx) {
		// Response already sent
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	req := actors.DeleteReminderRequest{
		Name:      name,
		ActorID:   actorID,
		ActorType: actorType,
	}

	err := a.universal.Actors().DeleteReminder(reqCtx, &req)
	if err != nil {
		if errors.Is(err, actors.ErrReminderOpActorNotHosted) {
			msg := messages.ErrActorReminderOpActorNotHosted
			universalFastHTTPErrorResponder(reqCtx, msg)
			log.Debug(msg)
			return
		}

		msg := messages.ErrActorReminderDelete.WithFormat(err)
		universalFastHTTPErrorResponder(reqCtx, msg)
		log.Debug(msg)
		return
	}

	fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
}

func (a *api) onActorStateTransaction(reqCtx *fasthttp.RequestCtx) {
	if !a.actorReadinessCheckFastHTTP(reqCtx) {
		// Response already sent
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	body := reqCtx.PostBody()

	var ops []actors.TransactionalOperation
	err := json.Unmarshal(body, &ops)
	if err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(http.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	hosted := a.universal.Actors().IsActorHosted(reqCtx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := messages.ErrActorInstanceMissing
		fasthttpRespond(reqCtx, fasthttpResponseWithError(http.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req := actors.TransactionalRequest{
		ActorID:    actorID,
		ActorType:  actorType,
		Operations: ops,
	}

	err = a.universal.Actors().TransactionalStateOperation(reqCtx, &req)
	if err != nil {
		msg := messages.ErrActorStateTransactionSave.WithFormat(err)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(http.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
}

func (a *api) onGetActorReminder(reqCtx *fasthttp.RequestCtx) {
	if !a.actorReadinessCheckFastHTTP(reqCtx) {
		// Response already sent
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	resp, err := a.universal.Actors().GetReminder(reqCtx, &actors.GetReminderRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Name:      name,
	})
	if err != nil {
		if errors.Is(err, actors.ErrReminderOpActorNotHosted) {
			msg := messages.ErrActorReminderOpActorNotHosted
			universalFastHTTPErrorResponder(reqCtx, msg)
			log.Debug(msg)
			return
		}

		msg := messages.ErrActorReminderGet.WithFormat(err)
		universalFastHTTPErrorResponder(reqCtx, msg)
		log.Debug(msg)
		return
	}

	b, err := json.Marshal(resp)
	if err != nil {
		msg := messages.ErrActorReminderGet.WithFormat(err)
		universalFastHTTPErrorResponder(reqCtx, msg)
		log.Debug(msg)
		return
	}

	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(http.StatusOK, b, nil))
}

func (a *api) onDeleteActorTimer(reqCtx *fasthttp.RequestCtx) {
	if !a.actorReadinessCheckFastHTTP(reqCtx) {
		// Response already sent
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	req := actors.DeleteTimerRequest{
		Name:      name,
		ActorID:   actorID,
		ActorType: actorType,
	}
	err := a.universal.Actors().DeleteTimer(reqCtx, &req)
	if err != nil {
		msg := messages.ErrActorTimerDelete.WithFormat(err)
		universalFastHTTPErrorResponder(reqCtx, msg)
		log.Debug(msg)
		return
	}
	fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
}

func (a *api) onDirectActorMessage(reqCtx *fasthttp.RequestCtx) {
	if !a.actorReadinessCheckFastHTTP(reqCtx) {
		// Response already sent
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	verb := strings.ToUpper(string(reqCtx.Method()))
	method := reqCtx.UserValue(methodParam).(string)

	policyDef := a.universal.Resiliency().ActorPreLockPolicy(actorType, actorID)

	req := internalsv1pb.NewInternalInvokeRequest(method).
		WithActor(actorType, actorID).
		WithHTTPExtension(verb, reqCtx.QueryArgs().String()).
		WithData(reqCtx.PostBody()).
		WithContentType(string(reqCtx.Request.Header.ContentType())).
		WithFastHTTPHeaders(&reqCtx.Request.Header)

	// Unlike other actor calls, resiliency is handled here for invocation.
	// This is due to actor invocation involving a lookup for the host.
	// Having the retry here allows us to capture that and be resilient to host failure.
	// Additionally, we don't perform timeouts at this level. This is because an actor
	// should technically wait forever on the locking mechanism. If we timeout while
	// waiting for the lock, we can also create a queue of calls that will try and continue
	// after the timeout.
	policyRunner := resiliency.NewRunner[*internalsv1pb.InternalInvokeResponse](reqCtx, policyDef)
	res, err := policyRunner(func(ctx context.Context) (*internalsv1pb.InternalInvokeResponse, error) {
		return a.universal.Actors().Call(ctx, req)
	})
	if err != nil {
		actorErr, isActorError := actorerrors.As(err)
		if !isActorError {
			msg := messages.ErrActorInvoke.WithFormat(err)
			fasthttpRespond(reqCtx, fasthttpResponseWithError(http.StatusInternalServerError, msg))
			log.Debug(msg)
			return
		}

		// Use Add to ensure headers are appended and not replaced
		invokev1.InternalMetadataToHTTPHeader(reqCtx, actorErr.Headers(), reqCtx.Response.Header.Add)
		reqCtx.Response.Header.SetContentType(actorErr.ContentType())

		// Construct response.
		statusCode := actorErr.StatusCode()
		body := actorErr.Body()
		fasthttpRespond(reqCtx, fasthttpResponseWith(statusCode, body))
		return
	}

	if res == nil {
		msg := messages.ErrActorInvoke.WithFormat("failed to cast response")
		fasthttpRespond(reqCtx, fasthttpResponseWithError(http.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	// Use Add to ensure headers are appended and not replaced
	invokev1.InternalMetadataToHTTPHeader(reqCtx, res.GetHeaders(), reqCtx.Response.Header.Add)
	body := res.GetMessage().GetData().GetValue()
	reqCtx.Response.Header.SetContentType(res.GetMessage().GetContentType())

	// Construct response.
	statusCode := int(res.GetStatus().GetCode())
	if !res.IsHTTPResponse() {
		statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
	}
	fasthttpRespond(reqCtx, fasthttpResponseWith(statusCode, body))
}

func (a *api) onGetActorState(reqCtx *fasthttp.RequestCtx) {
	if !a.actorReadinessCheckFastHTTP(reqCtx) {
		// Response already sent
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	key := reqCtx.UserValue(stateKeyParam).(string)

	hosted := a.universal.Actors().IsActorHosted(reqCtx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := messages.ErrActorInstanceMissing
		fasthttpRespond(reqCtx, fasthttpResponseWithError(http.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	resp, err := a.universal.Actors().GetState(reqCtx, &actors.GetStateRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Key:       key,
	})
	if err != nil {
		msg := messages.ErrActorStateGet.WithFormat(err)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(http.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		if resp == nil || len(resp.Data) == 0 {
			fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
			return
		}
		fasthttpRespond(reqCtx, fasthttpResponseWithJSON(http.StatusOK, resp.Data, resp.Metadata))
	}
}

// This function makes sure that the actor subsystem is ready.
// If it returns false, handlers should return without performing any other action: responses will be sent to the client already.
func (a *api) actorReadinessCheckFastHTTP(reqCtx *fasthttp.RequestCtx) bool {
	// Note: with FastHTTP, reqCtx is tied to the context of the *server* and not the request.
	// See: https://github.com/valyala/fasthttp/issues/1219#issuecomment-1041548933
	// So, this is effectively a background context when using FastHTTP.
	// There's no workaround besides migrating to the standard library's server.
	a.universal.WaitForActorsReady(reqCtx)
	if a.universal.Actors() == nil {
		universalFastHTTPErrorResponder(reqCtx, messages.ErrActorRuntimeNotFound)
		log.Debug(messages.ErrActorRuntimeNotFound)
		return false
	}
	return true
}
