// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/channel/http"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/google/uuid"
	jsoniter "github.com/json-iterator/go"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc/codes"
)

// API returns a list of HTTP endpoints for Dapr
type API interface {
	APIEndpoints() []Endpoint
	MarkStatusAsReady()
}

type api struct {
	endpoints             []Endpoint
	directMessaging       messaging.DirectMessaging
	appChannel            channel.AppChannel
	stateStores           map[string]state.Store
	secretStores          map[string]secretstores.SecretStore
	json                  jsoniter.API
	actor                 actors.Actors
	publishFn             func(req *pubsub.PublishRequest) error
	sendToOutputBindingFn func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	id                    string
	extendedMetadata      sync.Map
	readyStatus           bool
	tracingSpec           config.TracingSpec
}

type metadata struct {
	ID                string                      `json:"id"`
	ActiveActorsCount []actors.ActiveActorsCount  `json:"actors"`
	Extended          map[interface{}]interface{} `json:"extended"`
}

const (
	apiVersionV1         = "v1.0"
	idParam              = "id"
	methodParam          = "method"
	topicParam           = "topic"
	actorTypeParam       = "actorType"
	actorIDParam         = "actorId"
	storeNameParam       = "storeName"
	stateKeyParam        = "key"
	secretStoreNameParam = "secretStoreName"
	secretNameParam      = "key"
	nameParam            = "name"
	consistencyParam     = "consistency"
	retryIntervalParam   = "retryInterval"
	retryPatternParam    = "retryPattern"
	retryThresholdParam  = "retryThreshold"
	concurrencyParam     = "concurrency"
	daprSeparator        = "||"
)

// NewAPI returns a new API
func NewAPI(appID string, appChannel channel.AppChannel, directMessaging messaging.DirectMessaging, stateStores map[string]state.Store, secretStores map[string]secretstores.SecretStore, publishFn func(*pubsub.PublishRequest) error, actor actors.Actors, sendToOutputBindingFn func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error), tracingSpec config.TracingSpec) API {
	api := &api{
		appChannel:            appChannel,
		directMessaging:       directMessaging,
		stateStores:           stateStores,
		secretStores:          secretStores,
		json:                  jsoniter.ConfigFastest,
		actor:                 actor,
		publishFn:             publishFn,
		sendToOutputBindingFn: sendToOutputBindingFn,
		id:                    appID,
		tracingSpec:           tracingSpec,
	}
	api.endpoints = append(api.endpoints, api.constructStateEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructSecretEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructPubSubEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructActorEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructDirectMessagingEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructMetadataEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructBindingsEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructHealthzEndpoints()...)

	return api
}

// APIEndpoints returns the list of registered endpoints
func (a *api) APIEndpoints() []Endpoint {
	return a.endpoints
}

// MarkStatusAsReady marks the ready status of dapr
func (a *api) MarkStatusAsReady() {
	a.readyStatus = true
}

func (a *api) constructStateEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "state/{storeName}/{key}",
			Version: apiVersionV1,
			Handler: a.onGetState,
		},
		{
			Methods: []string{fasthttp.MethodPost},
			Route:   "state/{storeName}",
			Version: apiVersionV1,
			Handler: a.onPostState,
		},
		{
			Methods: []string{fasthttp.MethodDelete},
			Route:   "state/{storeName}/{key}",
			Version: apiVersionV1,
			Handler: a.onDeleteState,
		},
	}
}

func (a *api) constructSecretEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "secrets/{secretStoreName}/{key}",
			Version: apiVersionV1,
			Handler: a.onGetSecret,
		},
	}
}

func (a *api) constructPubSubEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "publish/{topic:*}",
			Version: apiVersionV1,
			Handler: a.onPublish,
		},
	}
}

func (a *api) constructBindingsEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "bindings/{name}",
			Version: apiVersionV1,
			Handler: a.onOutputBindingMessage,
		},
	}
}

func (a *api) constructDirectMessagingEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet, fasthttp.MethodPost, fasthttp.MethodDelete, fasthttp.MethodPut},
			Route:   "invoke/{id}/method/{method:*}",
			Version: apiVersionV1,
			Handler: a.onDirectMessage,
		},
	}
}

func (a *api) constructActorEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/state",
			Version: apiVersionV1,
			Handler: a.onActorStateTransaction,
		},
		{
			Methods: []string{fasthttp.MethodGet, fasthttp.MethodPost, fasthttp.MethodDelete, fasthttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/method/{method}",
			Version: apiVersionV1,
			Handler: a.onDirectActorMessage,
		},
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/state/{key}",
			Version: apiVersionV1,
			Handler: a.onSaveActorState,
		},
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "actors/{actorType}/{actorId}/state/{key}",
			Version: apiVersionV1,
			Handler: a.onGetActorState,
		},
		{
			Methods: []string{fasthttp.MethodDelete},
			Route:   "actors/{actorType}/{actorId}/state/{key}",
			Version: apiVersionV1,
			Handler: a.onDeleteActorState,
		},
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Handler: a.onCreateActorReminder,
		},
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/timers/{name}",
			Version: apiVersionV1,
			Handler: a.onCreateActorTimer,
		},
		{
			Methods: []string{fasthttp.MethodDelete},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Handler: a.onDeleteActorReminder,
		},
		{
			Methods: []string{fasthttp.MethodDelete},
			Route:   "actors/{actorType}/{actorId}/timers/{name}",
			Version: apiVersionV1,
			Handler: a.onDeleteActorTimer,
		},
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Handler: a.onGetActorReminder,
		},
	}
}

func (a *api) constructMetadataEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "metadata",
			Version: apiVersionV1,
			Handler: a.onGetMetadata,
		},
		{
			Methods: []string{fasthttp.MethodPut},
			Route:   "metadata/{key}",
			Version: apiVersionV1,
			Handler: a.onPutMetadata,
		},
	}
}

func (a *api) constructHealthzEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "healthz",
			Version: apiVersionV1,
			Handler: a.onGetHealthz,
		},
	}
}

func (a *api) onOutputBindingMessage(reqCtx *fasthttp.RequestCtx) {
	name := reqCtx.UserValue(nameParam).(string)
	body := reqCtx.PostBody()

	var req OutputBindingRequest
	err := a.json.Unmarshal(body, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", fmt.Sprintf("can't deserialize request: %s", err))
		respondWithError(reqCtx, 500, msg)
		return
	}

	b, err := a.json.Marshal(req.Data)
	if err != nil {
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", fmt.Sprintf("can't deserialize request data field: %s", err))
		respondWithError(reqCtx, 500, msg)
		return
	}

	resp, err := a.sendToOutputBindingFn(name, &bindings.InvokeRequest{
		Metadata:  req.Metadata,
		Data:      b,
		Operation: bindings.OperationKind(req.Operation),
	})
	if err != nil {
		errMsg := fmt.Sprintf("error invoking output binding %s: %s", name, err)
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", errMsg)
		respondWithError(reqCtx, 500, msg)
		return
	}
	if resp == nil {
		respondEmpty(reqCtx, 200)
	} else {
		respondWithJSON(reqCtx, 200, resp.Data)
	}
}

func (a *api) onGetState(reqCtx *fasthttp.RequestCtx) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_CONFIGURED", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	storeName := reqCtx.UserValue(storeNameParam).(string)

	if a.stateStores[storeName] == nil {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf("state store name: %s", storeName))
		respondWithError(reqCtx, 400, msg)
		return
	}

	metadata := getMetadataFromRequest(reqCtx)

	key := reqCtx.UserValue(stateKeyParam).(string)
	consistency := string(reqCtx.QueryArgs().Peek(consistencyParam))
	req := state.GetRequest{
		Key: a.getModifiedStateKey(key),
		Options: state.GetStateOption{
			Consistency: consistency,
		},
		Metadata: metadata,
	}

	resp, err := a.stateStores[storeName].Get(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_STATE_GET", err.Error())
		respondWithError(reqCtx, 400, msg)
		return
	}
	if resp == nil || resp.Data == nil {
		respondEmpty(reqCtx, 204)
		return
	}
	respondWithETaggedJSON(reqCtx, 200, resp.Data, resp.ETag)
}

func (a *api) onDeleteState(reqCtx *fasthttp.RequestCtx) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORES_NOT_CONFIGURED", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	storeName := reqCtx.UserValue(storeNameParam).(string)

	if a.stateStores[storeName] == nil {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf("state store name: %s", storeName))
		respondWithError(reqCtx, 401, msg)
		return
	}

	key := reqCtx.UserValue(stateKeyParam).(string)
	etag := string(reqCtx.Request.Header.Peek("If-Match"))

	concurrency := string(reqCtx.QueryArgs().Peek(concurrencyParam))
	consistency := string(reqCtx.QueryArgs().Peek(consistencyParam))
	retryInterval := string(reqCtx.QueryArgs().Peek(retryIntervalParam))
	retryPattern := string(reqCtx.QueryArgs().Peek(retryPatternParam))
	retryThredhold := string(reqCtx.QueryArgs().Peek(retryThresholdParam))
	iRetryInterval := 0
	iRetryThreshold := 0

	if retryInterval != "" {
		iRetryInterval, _ = strconv.Atoi(retryInterval)
	}
	if retryThredhold != "" {
		iRetryThreshold, _ = strconv.Atoi(retryThredhold)
	}

	req := state.DeleteRequest{
		Key:  a.getModifiedStateKey(key),
		ETag: etag,
		Options: state.DeleteStateOption{
			Concurrency: concurrency,
			Consistency: consistency,
			RetryPolicy: state.RetryPolicy{
				Interval:  time.Duration(iRetryInterval) * time.Millisecond,
				Threshold: iRetryThreshold,
				Pattern:   retryPattern,
			},
		},
	}

	err := a.stateStores[storeName].Delete(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_STATE_DELETE", fmt.Sprintf("failed deleting state with key %s: %s", key, err))
		respondWithError(reqCtx, 500, msg)
		return
	}
	respondEmpty(reqCtx, 200)
}

func (a *api) onGetSecret(reqCtx *fasthttp.RequestCtx) {
	if a.secretStores == nil || len(a.secretStores) == 0 {
		msg := NewErrorResponse("ERR_SECRET_STORE_NOT_CONFIGURED", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	secretStoreName := reqCtx.UserValue(secretStoreNameParam).(string)

	if a.secretStores[secretStoreName] == nil {
		msg := NewErrorResponse("ERR_SECRET_STORE_NOT_FOUND", fmt.Sprintf("secret store name: %s", secretStoreName))
		respondWithError(reqCtx, 401, msg)
		return
	}

	metadata := getMetadataFromRequest(reqCtx)

	key := reqCtx.UserValue(secretNameParam).(string)
	req := secretstores.GetSecretRequest{
		Name:     key,
		Metadata: metadata,
	}

	resp, err := a.secretStores[secretStoreName].GetSecret(req)
	if err != nil {
		msg := NewErrorResponse("ERR_STATE_GET", err.Error())
		respondWithError(reqCtx, 500, msg)
		return
	}

	if resp.Data == nil {
		respondEmpty(reqCtx, 204)
		return
	}

	respBytes, _ := a.json.Marshal(resp.Data)
	respondWithJSON(reqCtx, 200, respBytes)
}

func (a *api) onPostState(reqCtx *fasthttp.RequestCtx) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORES_NOT_CONFIGURED", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	storeName := reqCtx.UserValue(storeNameParam).(string)

	if a.stateStores[storeName] == nil {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf("state store name: %s", storeName))
		respondWithError(reqCtx, 401, msg)
		return
	}

	reqs := []state.SetRequest{}
	err := a.json.Unmarshal(reqCtx.PostBody(), &reqs)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(reqCtx, 400, msg)
		return
	}

	for i, r := range reqs {
		reqs[i].Key = a.getModifiedStateKey(r.Key)
	}

	err = a.stateStores[storeName].BulkSet(reqs)
	if err != nil {
		msg := NewErrorResponse("ERR_STATE_SAVE", err.Error())
		respondWithError(reqCtx, 500, msg)
		return
	}

	respondEmpty(reqCtx, 201)
}

func (a *api) getModifiedStateKey(key string) string {
	if a.id != "" {
		return fmt.Sprintf("%s%s%s", a.id, daprSeparator, key)
	}

	return key
}

func (a *api) onDirectMessage(reqCtx *fasthttp.RequestCtx) {
	targetID := reqCtx.UserValue(idParam).(string)
	verb := strings.ToUpper(string(reqCtx.Method()))
	invokeMethodName := reqCtx.UserValue(methodParam).(string)
	if invokeMethodName == "" {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", "invalid method name")
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		return
	}

	// Construct internal invoke method request
	req := invokev1.NewInvokeMethodRequest(invokeMethodName).WithHTTPExtension(verb, reqCtx.QueryArgs().String())
	req.WithRawData(reqCtx.Request.Body(), string(reqCtx.Request.Header.ContentType()))
	// Save headers to internal metadata
	req.WithFastHTTPHeaders(&reqCtx.Request.Header)

	resp, err := a.directMessaging.Invoke(reqCtx, targetID, req)
	// err does not represent user application response
	if err != nil {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", err.Error())
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		return
	}

	invokev1.InternalMetadataToHTTPHeader(reqCtx, resp.Headers(), reqCtx.Response.Header.Set)
	contentType, body := resp.RawData()
	reqCtx.Response.Header.SetContentType(contentType)

	// Construct response
	statusCode := int(resp.Status().Code)
	if !resp.IsHTTPResponse() {
		statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
	}

	respond(reqCtx, statusCode, body)
}

func (a *api) onCreateActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	var req actors.CreateReminderRequest
	err := a.json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(reqCtx, 400, msg)
		return
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.CreateReminder(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_CREATE", err.Error())
		respondWithError(reqCtx, 500, msg)
	} else {
		respondEmpty(reqCtx, 200)
	}
}

func (a *api) onCreateActorTimer(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	var req actors.CreateTimerRequest
	err := a.json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(reqCtx, 400, msg)
		return
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.CreateTimer(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_TIMER_CREATE", err.Error())
		respondWithError(reqCtx, 500, msg)
	} else {
		respondEmpty(reqCtx, 200)
	}
}

func (a *api) onDeleteActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(reqCtx, 400, msg)
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

	err := a.actor.DeleteReminder(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_DELETE", err.Error())
		respondWithError(reqCtx, 500, msg)
	} else {
		respondEmpty(reqCtx, 200)
	}
}

func (a *api) onActorStateTransaction(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	body := reqCtx.PostBody()

	hosted := a.actor.IsActorHosted(reqCtx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	var ops []actors.TransactionalOperation
	err := a.json.Unmarshal(body, &ops)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(reqCtx, 400, msg)
		return
	}

	req := actors.TransactionalRequest{
		ActorID:    actorID,
		ActorType:  actorType,
		Operations: ops,
	}

	err = a.actor.TransactionalStateOperation(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_TRANSACTION_SAVE", err.Error())
		respondWithError(reqCtx, 500, msg)
	} else {
		respondEmpty(reqCtx, 201)
	}
}

func (a *api) onGetActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	resp, err := a.actor.GetReminder(reqCtx, &actors.GetReminderRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Name:      name,
	})
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", err.Error())
		respondWithError(reqCtx, 500, msg)
	}
	b, err := a.json.Marshal(resp)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", err.Error())
		respondWithError(reqCtx, 500, msg)
	} else {
		respondWithJSON(reqCtx, 200, b)
	}
}

func (a *api) onDeleteActorTimer(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(reqCtx, 400, msg)
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
	err := a.actor.DeleteTimer(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_TIMER_DELETE", err.Error())
		respondWithError(reqCtx, 500, msg)
	} else {
		respondEmpty(reqCtx, 200)
	}
}

func (a *api) onDirectActorMessage(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	verb := strings.ToUpper(string(reqCtx.Method()))
	method := reqCtx.UserValue(methodParam).(string)
	body := reqCtx.PostBody()

	req := invokev1.NewInvokeMethodRequest(method)
	req.WithActor(actorType, actorID)
	req.WithHTTPExtension(verb, reqCtx.QueryArgs().String())
	req.WithRawData(body, string(reqCtx.Request.Header.ContentType()))

	// Save headers to metadata
	metadata := map[string][]string{}
	reqCtx.Request.Header.VisitAll(func(key []byte, value []byte) {
		metadata[string(key)] = []string{string(value)}
	})
	req.WithMetadata(metadata)

	resp, err := a.actor.Call(reqCtx, req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", err.Error())
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		return
	}

	invokev1.InternalMetadataToHTTPHeader(reqCtx, resp.Headers(), reqCtx.Response.Header.Set)
	contentType, body := resp.RawData()
	reqCtx.Response.Header.SetContentType(contentType)

	// Construct response
	statusCode := int(resp.Status().Code)
	if !resp.IsHTTPResponse() {
		statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
	}
	respond(reqCtx, statusCode, body)
}

func (a *api) onSaveActorState(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	key := reqCtx.UserValue(stateKeyParam).(string)
	body := reqCtx.PostBody()

	hosted := a.actor.IsActorHosted(reqCtx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	// Deserialize body to validate JSON compatible body
	// and remove useless characters before saving
	var val interface{}
	err := a.json.Unmarshal(body, &val)
	if err != nil {
		msg := NewErrorResponse("ERR_DESERIALIZE_HTTP_BODY", err.Error())
		respondWithError(reqCtx, 400, msg)
		return
	}

	req := actors.SaveStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       key,
		Value:     val,
	}

	err = a.actor.SaveState(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_SAVE", err.Error())
		respondWithError(reqCtx, 500, msg)
	} else {
		respondEmpty(reqCtx, 201)
	}
}

func (a *api) onGetActorState(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	key := reqCtx.UserValue(stateKeyParam).(string)

	req := actors.GetStateRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Key:       key,
	}

	resp, err := a.actor.GetState(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_GET", err.Error())
		respondWithError(reqCtx, 500, msg)
	} else {
		respondWithJSON(reqCtx, 200, resp.Data)
	}
}

func (a *api) onDeleteActorState(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	key := reqCtx.UserValue(stateKeyParam).(string)

	hosted := a.actor.IsActorHosted(reqCtx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	req := actors.DeleteStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       key,
	}

	err := a.actor.DeleteState(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_DELETE", err.Error())
		respondWithError(reqCtx, 500, msg)
	} else {
		respondEmpty(reqCtx, 200)
	}
}

func (a *api) onGetMetadata(reqCtx *fasthttp.RequestCtx) {
	temp := make(map[interface{}]interface{})

	// Copy synchronously so it can be serialized to JSON.
	a.extendedMetadata.Range(func(key, value interface{}) bool {
		temp[key] = value
		return true
	})

	mtd := metadata{
		ID:                a.id,
		ActiveActorsCount: a.actor.GetActiveActorsCount(reqCtx),
		Extended:          temp,
	}

	mtdBytes, err := a.json.Marshal(mtd)
	if err != nil {
		msg := NewErrorResponse("ERR_METADATA_GET", err.Error())
		respondWithError(reqCtx, 500, msg)
	} else {
		respondWithJSON(reqCtx, 200, mtdBytes)
	}
}

func (a *api) onPutMetadata(reqCtx *fasthttp.RequestCtx) {
	key := fmt.Sprintf("%v", reqCtx.UserValue("key"))
	body := reqCtx.PostBody()
	a.extendedMetadata.Store(key, string(body))
	respondEmpty(reqCtx, 200)
}

func (a *api) onPublish(reqCtx *fasthttp.RequestCtx) {
	if a.publishFn == nil {
		msg := NewErrorResponse("ERR_PUBSUB_NOT_FOUND", "")
		respondWithError(reqCtx, 400, msg)
		return
	}

	topic := reqCtx.UserValue(topicParam).(string)
	body := reqCtx.PostBody()

	// Extract trace context from context.
	span := diag_utils.SpanFromContext(reqCtx)
	// Populate W3C traceparent to cloudevent envelope
	corID := diag.SpanContextToW3CString(span.SpanContext())
	envelope := pubsub.NewCloudEventsEnvelope(uuid.New().String(), a.id, pubsub.DefaultCloudEventType, corID, body)

	b, err := a.json.Marshal(envelope)
	if err != nil {
		msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER", err.Error())
		respondWithError(reqCtx, 500, msg)
		return
	}

	req := pubsub.PublishRequest{
		Topic: topic,
		Data:  b,
	}

	err = a.publishFn(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_PUBSUB_PUBLISH_MESSAGE", err.Error())
		respondWithError(reqCtx, 500, msg)
	} else {
		respondEmpty(reqCtx, 200)
	}
}

// GetStatusCodeFromMetadata extracts the http status code from the metadata if it exists
func GetStatusCodeFromMetadata(metadata map[string]string) int {
	code := metadata[http.HTTPStatusCode]
	if code != "" {
		statusCode, err := strconv.Atoi(code)
		if err == nil {
			return statusCode
		}
	}
	return 200
}

func (a *api) onGetHealthz(reqCtx *fasthttp.RequestCtx) {
	if !a.readyStatus {
		msg := NewErrorResponse("ERR_HEALTH_NOT_READY", "dapr is not ready")
		respondWithError(reqCtx, 500, msg)
	} else {
		respondEmpty(reqCtx, 200)
	}
}

func getMetadataFromRequest(reqCtx *fasthttp.RequestCtx) map[string]string {
	metadata := map[string]string{}
	const metadataPrefix string = "metadata."
	reqCtx.QueryArgs().VisitAll(func(key []byte, value []byte) {
		queryKey := string(key)
		if strings.HasPrefix(queryKey, metadataPrefix) {
			k := strings.TrimPrefix(queryKey, metadataPrefix)
			metadata[k] = string(value)
		}
	})

	return metadata
}
