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
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/actors"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/api/http/endpoints"
	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
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
			Methods: []string{http.MethodPost, http.MethodPut},
			Route:   "actors/{actorType}/{actorId}/state",
			Version: apiVersionV1,
			Group:   endpointGroupActorV1State,
			Handler: a.onActorStateTransaction,
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
			Handler: a.onDirectActorMessage,
			Settings: endpoints.EndpointSettings{
				Name: "InvokeActor",
			},
		},
		{
			Methods: []string{http.MethodGet},
			Route:   "actors/{actorType}/{actorId}/state/{key}",
			Version: apiVersionV1,
			Group:   endpointGroupActorV1State,
			Handler: a.onGetActorState,
			Settings: endpoints.EndpointSettings{
				Name: "GetActorState",
			},
		},
		{
			Methods: []string{http.MethodPost, http.MethodPut},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Group:   endpointGroupActorV1Misc,
			Handler: a.onCreateActorReminder,
			Settings: endpoints.EndpointSettings{
				Name: "RegisterActorReminder",
			},
		},
		{
			Methods: []string{http.MethodPost, http.MethodPut},
			Route:   "actors/{actorType}/{actorId}/timers/{name}",
			Version: apiVersionV1,
			Group:   endpointGroupActorV1Misc,
			Handler: a.onCreateActorTimer,
			Settings: endpoints.EndpointSettings{
				Name: "RegisterActorTimer",
			},
		},
		{
			Methods: []string{http.MethodDelete},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Group:   endpointGroupActorV1Misc,
			Handler: a.onDeleteActorReminder(),
			Settings: endpoints.EndpointSettings{
				Name: "UnregisterActorReminder",
			},
		},
		{
			Methods: []string{http.MethodDelete},
			Route:   "actors/{actorType}/{actorId}/timers/{name}",
			Version: apiVersionV1,
			Group:   endpointGroupActorV1Misc,
			Handler: a.onDeleteActorTimer(),
			Settings: endpoints.EndpointSettings{
				Name: "UnregisterActorTimer",
			},
		},
		{
			Methods: []string{http.MethodGet},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Group:   endpointGroupActorV1Misc,
			Handler: a.onGetActorReminder,
			Settings: endpoints.EndpointSettings{
				Name: "GetActorReminder",
			},
		},
	}
}

func (a *api) onCreateActorReminder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := a.universal.ActorReadinessCheck(ctx)
	if err != nil {
		respondWithError(w, err)
		return
	}

	var req actors.CreateReminderRequest
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	req.Name = chi.URLParamFromCtx(ctx, nameParam)
	req.ActorType = chi.URLParamFromCtx(ctx, actorTypeParam)
	req.ActorID = chi.URLParamFromCtx(ctx, actorIDParam)

	err = a.universal.Actors().CreateReminder(ctx, &req)
	if err != nil {
		if errors.Is(err, actors.ErrReminderOpActorNotHosted) {
			msg := messages.ErrActorReminderOpActorNotHosted
			respondWithError(w, msg)
			log.Debug(msg)
			return
		}

		msg := messages.ErrActorReminderCreate.WithFormat(err)
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	respondWithEmpty(w)
}

func (a *api) onCreateActorTimer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := a.universal.ActorReadinessCheck(ctx)
	if err != nil {
		respondWithError(w, err)
		return
	}

	var req actors.CreateTimerRequest
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	req.Name = chi.URLParamFromCtx(ctx, nameParam)
	req.ActorType = chi.URLParamFromCtx(ctx, actorTypeParam)
	req.ActorID = chi.URLParamFromCtx(ctx, actorIDParam)

	err = a.universal.Actors().CreateTimer(ctx, &req)
	if err != nil {
		msg := messages.ErrActorTimerCreate.WithFormat(err)
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	respondWithEmpty(w)
}

func (a *api) onDeleteActorReminder() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.UnregisterActorReminder,
		UniversalHTTPHandlerOpts[*runtimev1pb.UnregisterActorReminderRequest, *emptypb.Empty]{
			InModifier: func(r *http.Request, in *runtimev1pb.UnregisterActorReminderRequest) (*runtimev1pb.UnregisterActorReminderRequest, error) {
				in.ActorType = chi.URLParam(r, actorTypeParam)
				in.ActorId = chi.URLParam(r, actorIDParam)
				in.Name = chi.URLParam(r, nameParam)
				return in, nil
			},
		},
	)
}

func (a *api) onActorStateTransaction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := a.universal.ActorReadinessCheck(ctx)
	if err != nil {
		respondWithError(w, err)
		return
	}

	actorType := chi.URLParamFromCtx(ctx, actorTypeParam)
	actorID := chi.URLParamFromCtx(ctx, actorIDParam)

	var ops []actors.TransactionalOperation
	err = json.NewDecoder(r.Body).Decode(&ops)
	if err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	hosted := a.universal.Actors().IsActorHosted(ctx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := messages.ErrActorInstanceMissing
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	req := actors.TransactionalRequest{
		ActorID:    actorID,
		ActorType:  actorType,
		Operations: ops,
	}

	err = a.universal.Actors().TransactionalStateOperation(ctx, &req)
	if err != nil {
		msg := messages.ErrActorStateTransactionSave.WithFormat(err)
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	respondWithEmpty(w)
}

func (a *api) onGetActorReminder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := a.universal.ActorReadinessCheck(ctx)
	if err != nil {
		respondWithError(w, err)
		return
	}

	resp, err := a.universal.Actors().GetReminder(ctx, &actors.GetReminderRequest{
		ActorType: chi.URLParamFromCtx(ctx, actorTypeParam),
		ActorID:   chi.URLParamFromCtx(ctx, actorIDParam),
		Name:      chi.URLParamFromCtx(ctx, nameParam),
	})
	if err != nil {
		if errors.Is(err, actors.ErrReminderOpActorNotHosted) {
			msg := messages.ErrActorReminderOpActorNotHosted
			respondWithError(w, msg)
			log.Debug(msg)
			return
		}

		msg := messages.ErrActorReminderGet.WithFormat(err)
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	respondWithJSON(w, http.StatusOK, resp)
}

func (a *api) onDeleteActorTimer() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.UnregisterActorTimer,
		UniversalHTTPHandlerOpts[*runtimev1pb.UnregisterActorTimerRequest, *emptypb.Empty]{
			InModifier: func(r *http.Request, in *runtimev1pb.UnregisterActorTimerRequest) (*runtimev1pb.UnregisterActorTimerRequest, error) {
				in.ActorType = chi.URLParam(r, actorTypeParam)
				in.ActorId = chi.URLParam(r, actorIDParam)
				in.Name = chi.URLParam(r, nameParam)
				return in, nil
			},
		},
	)
}

func (a *api) onDirectActorMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := a.universal.ActorReadinessCheck(ctx)
	if err != nil {
		respondWithError(w, err)
		return
	}

	actorType := chi.URLParamFromCtx(ctx, actorTypeParam)
	actorID := chi.URLParamFromCtx(ctx, actorIDParam)
	verb := strings.ToUpper(r.Method)
	method := chi.URLParamFromCtx(ctx, methodParam)

	policyDef := a.universal.Resiliency().ActorPreLockPolicy(actorType, actorID)

	// Actor invocation doesn't support streaming, so we need to read the entire reqBody
	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		msg := messages.ErrBadRequest.WithFormat("failed to read body: " + err.Error())
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	req := internalsv1pb.NewInternalInvokeRequest(method).
		WithActor(actorType, actorID).
		WithHTTPExtension(verb, r.URL.RawQuery).
		WithData(reqBody).
		WithContentType(r.Header.Get("content-type")).
		// Save headers to internal metadata
		WithHTTPHeaders(r.Header)

	// Unlike other actor calls, resiliency is handled here for invocation.
	// This is due to actor invocation involving a lookup for the host.
	// Having the retry here allows us to capture that and be resilient to host failure.
	// Additionally, we don't perform timeouts at this level. This is because an actor
	// should technically wait forever on the locking mechanism. If we timeout while
	// waiting for the lock, we can also create a queue of calls that will try and continue
	// after the timeout.
	policyRunner := resiliency.NewRunner[*internalsv1pb.InternalInvokeResponse](ctx, policyDef)
	res, err := policyRunner(func(ctx context.Context) (*internalsv1pb.InternalInvokeResponse, error) {
		return a.universal.Actors().Call(ctx, req)
	})
	if err != nil {
		actorErr, isActorError := actorerrors.As(err)
		if !isActorError {
			msg := messages.ErrActorInvoke.WithFormat(err)
			respondWithError(w, msg)
			log.Debug(msg)
			return
		}

		// Use Add to ensure headers are appended and not replaced
		h := w.Header()
		invokev1.InternalMetadataToHTTPHeader(ctx, actorErr.Headers(), h.Add)
		h.Set(headerContentType, actorErr.ContentType())

		// Construct response
		respondWithData(w, actorErr.StatusCode(), actorErr.Body())
		return
	}

	if res == nil {
		msg := messages.ErrActorInvoke.WithFormat("failed to cast response")
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	// Use Add to ensure headers are appended and not replaced
	h := w.Header()
	invokev1.InternalMetadataToHTTPHeader(ctx, res.GetHeaders(), h.Add)
	h.Set(headerContentType, res.GetMessage().GetContentType())

	// Construct response.
	statusCode := int(res.GetStatus().GetCode())
	if !res.IsHTTPResponse() {
		statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
	}
	respondWithData(w, statusCode, res.GetMessage().GetData().GetValue())
}

func (a *api) onGetActorState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := a.universal.ActorReadinessCheck(ctx)
	if err != nil {
		respondWithError(w, err)
		return
	}

	actorType := chi.URLParamFromCtx(ctx, actorTypeParam)
	actorID := chi.URLParamFromCtx(ctx, actorIDParam)
	key := chi.URLParamFromCtx(ctx, stateKeyParam)

	hosted := a.universal.Actors().IsActorHosted(ctx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := messages.ErrActorInstanceMissing
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	resp, err := a.universal.Actors().GetState(ctx, &actors.GetStateRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Key:       key,
	})
	if err != nil {
		msg := messages.ErrActorStateGet.WithFormat(err)
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	if resp == nil || len(resp.Data) == 0 {
		respondWithEmpty(w)
		return
	}

	// Set headers
	h := w.Header()
	h.Set(headerContentType, jsonContentTypeHeader)
	setResponseMetadataHeaders(w, resp.Metadata)

	respondWithData(w, http.StatusOK, resp.Data)
}
