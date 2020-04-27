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
	tracing "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/google/uuid"
	jsoniter "github.com/json-iterator/go"
	"github.com/valyala/fasthttp"
	fhttp "github.com/valyala/fasthttp"
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
	sendToOutputBindingFn func(name string, req *bindings.WriteRequest) error
	id                    string
	extendedMetadata      sync.Map
	readyStatus           bool
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
func NewAPI(appID string, appChannel channel.AppChannel, directMessaging messaging.DirectMessaging, stateStores map[string]state.Store, secretStores map[string]secretstores.SecretStore, publishFn func(*pubsub.PublishRequest) error, actor actors.Actors, sendToOutputBindingFn func(name string, req *bindings.WriteRequest) error) API {
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
			Methods: []string{fhttp.MethodGet},
			Route:   "state/{storeName}/{key}",
			Version: apiVersionV1,
			Handler: a.onGetState,
		},
		{
			Methods: []string{fhttp.MethodPost},
			Route:   "state/{storeName}",
			Version: apiVersionV1,
			Handler: a.onPostState,
		},
		{
			Methods: []string{fhttp.MethodDelete},
			Route:   "state/{storeName}/{key}",
			Version: apiVersionV1,
			Handler: a.onDeleteState,
		},
	}
}

func (a *api) constructSecretEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fhttp.MethodGet},
			Route:   "secrets/{secretStoreName}/{key}",
			Version: apiVersionV1,
			Handler: a.onGetSecret,
		},
	}
}

func (a *api) constructPubSubEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fhttp.MethodPost, fhttp.MethodPut},
			Route:   "publish/{topic:*}",
			Version: apiVersionV1,
			Handler: a.onPublish,
		},
	}
}

func (a *api) constructBindingsEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fhttp.MethodPost, fhttp.MethodPut},
			Route:   "bindings/{name}",
			Version: apiVersionV1,
			Handler: a.onOutputBindingMessage,
		},
	}
}

func (a *api) constructDirectMessagingEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fhttp.MethodGet, fhttp.MethodPost, fhttp.MethodDelete, fhttp.MethodPut},
			Route:   "invoke/{id}/method/{method:*}",
			Version: apiVersionV1,
			Handler: a.onDirectMessage,
		},
	}
}

func (a *api) constructActorEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fhttp.MethodPost, fhttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/state",
			Version: apiVersionV1,
			Handler: a.onActorStateTransaction,
		},
		{
			Methods: []string{fhttp.MethodGet, fhttp.MethodPost, fhttp.MethodDelete, fhttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/method/{method}",
			Version: apiVersionV1,
			Handler: a.onDirectActorMessage,
		},
		{
			Methods: []string{fhttp.MethodPost, fhttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/state/{key}",
			Version: apiVersionV1,
			Handler: a.onSaveActorState,
		},
		{
			Methods: []string{fhttp.MethodGet},
			Route:   "actors/{actorType}/{actorId}/state/{key}",
			Version: apiVersionV1,
			Handler: a.onGetActorState,
		},
		{
			Methods: []string{fhttp.MethodDelete},
			Route:   "actors/{actorType}/{actorId}/state/{key}",
			Version: apiVersionV1,
			Handler: a.onDeleteActorState,
		},
		{
			Methods: []string{fhttp.MethodPost, fhttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Handler: a.onCreateActorReminder,
		},
		{
			Methods: []string{fhttp.MethodPost, fhttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/timers/{name}",
			Version: apiVersionV1,
			Handler: a.onCreateActorTimer,
		},
		{
			Methods: []string{fhttp.MethodDelete},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Handler: a.onDeleteActorReminder,
		},
		{
			Methods: []string{fhttp.MethodDelete},
			Route:   "actors/{actorType}/{actorId}/timers/{name}",
			Version: apiVersionV1,
			Handler: a.onDeleteActorTimer,
		},
		{
			Methods: []string{fhttp.MethodGet},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Handler: a.onGetActorReminder,
		},
	}
}

func (a *api) constructMetadataEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fhttp.MethodGet},
			Route:   "metadata",
			Version: apiVersionV1,
			Handler: a.onGetMetadata,
		},
		{
			Methods: []string{fhttp.MethodPut},
			Route:   "metadata/{key}",
			Version: apiVersionV1,
			Handler: a.onPutMetadata,
		},
	}
}

func (a *api) constructHealthzEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fhttp.MethodGet},
			Route:   "healthz",
			Version: apiVersionV1,
			Handler: a.onGetHealthz,
		},
	}
}

func (a *api) onOutputBindingMessage(ctx *fasthttp.RequestCtx) {
	name := ctx.UserValue(nameParam).(string)
	body := ctx.PostBody()

	var req OutputBindingRequest
	err := a.json.Unmarshal(body, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", fmt.Sprintf("can't deserialize request: %s", err))
		respondWithError(ctx, 500, msg)
		return
	}

	b, err := a.json.Marshal(req.Data)
	if err != nil {
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", fmt.Sprintf("can't deserialize request data field: %s", err))
		respondWithError(ctx, 500, msg)
		return
	}
	err = a.sendToOutputBindingFn(name, &bindings.WriteRequest{
		Metadata: req.Metadata,
		Data:     b,
	})
	if err != nil {
		errMsg := fmt.Sprintf("error invoking output binding %s: %s", name, err)
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", errMsg)
		respondWithError(ctx, 500, msg)
		return
	}
	respondEmpty(ctx, 200)
}

func (a *api) onGetState(ctx *fasthttp.RequestCtx) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_CONFIGURED", "")
		respondWithError(ctx, 400, msg)
		return
	}

	storeName := ctx.UserValue(storeNameParam).(string)

	if a.stateStores[storeName] == nil {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf("state store name: %s", storeName))
		respondWithError(ctx, 401, msg)
		return
	}

	key := ctx.UserValue(stateKeyParam).(string)
	consistency := string(ctx.QueryArgs().Peek(consistencyParam))
	req := state.GetRequest{
		Key: a.getModifiedStateKey(key),
		Options: state.GetStateOption{
			Consistency: consistency,
		},
	}

	resp, err := a.stateStores[storeName].Get(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_STATE_GET", err.Error())
		respondWithError(ctx, 500, msg)
		return
	}
	if resp == nil || resp.Data == nil {
		respondEmpty(ctx, 204)
		return
	}
	respondWithETaggedJSON(ctx, 200, resp.Data, resp.ETag)
}

func (a *api) onDeleteState(ctx *fasthttp.RequestCtx) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORES_NOT_CONFIGURED", "")
		respondWithError(ctx, 400, msg)
		return
	}

	storeName := ctx.UserValue(storeNameParam).(string)

	if a.stateStores[storeName] == nil {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf("state store name: %s", storeName))
		respondWithError(ctx, 401, msg)
		return
	}

	key := ctx.UserValue(stateKeyParam).(string)
	etag := string(ctx.Request.Header.Peek("If-Match"))

	concurrency := string(ctx.QueryArgs().Peek(concurrencyParam))
	consistency := string(ctx.QueryArgs().Peek(consistencyParam))
	retryInterval := string(ctx.QueryArgs().Peek(retryIntervalParam))
	retryPattern := string(ctx.QueryArgs().Peek(retryPatternParam))
	retryThredhold := string(ctx.QueryArgs().Peek(retryThresholdParam))
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
		respondWithError(ctx, 500, msg)
		return
	}
	respondEmpty(ctx, 200)
}

func (a *api) onGetSecret(ctx *fasthttp.RequestCtx) {
	if a.secretStores == nil || len(a.secretStores) == 0 {
		msg := NewErrorResponse("ERR_SECRET_STORE_NOT_CONFIGURED", "")
		respondWithError(ctx, 400, msg)
		return
	}

	secretStoreName := ctx.UserValue(secretStoreNameParam).(string)

	if a.secretStores[secretStoreName] == nil {
		msg := NewErrorResponse("ERR_SECRET_STORE_NOT_FOUND", fmt.Sprintf("secret store name: %s", secretStoreName))
		respondWithError(ctx, 401, msg)
		return
	}

	metadata := map[string]string{}
	const metadataPrefix string = "metadata."
	ctx.QueryArgs().VisitAll(func(key []byte, value []byte) {
		queryKey := string(key)
		if strings.HasPrefix(queryKey, metadataPrefix) {
			k := strings.TrimPrefix(queryKey, metadataPrefix)
			metadata[k] = string(value)
		}
	})

	key := ctx.UserValue(secretNameParam).(string)
	req := secretstores.GetSecretRequest{
		Name:     key,
		Metadata: metadata,
	}

	resp, err := a.secretStores[secretStoreName].GetSecret(req)
	if err != nil {
		msg := NewErrorResponse("ERR_STATE_GET", err.Error())
		respondWithError(ctx, 500, msg)
		return
	}

	if resp.Data == nil {
		respondEmpty(ctx, 204)
		return
	}

	respBytes, _ := a.json.Marshal(resp.Data)
	respondWithJSON(ctx, 200, respBytes)
}

func (a *api) onPostState(ctx *fasthttp.RequestCtx) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORES_NOT_CONFIGURED", "")
		respondWithError(ctx, 400, msg)
		return
	}

	storeName := ctx.UserValue(storeNameParam).(string)

	if a.stateStores[storeName] == nil {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf("state store name: %s", storeName))
		respondWithError(ctx, 401, msg)
		return
	}

	reqs := []state.SetRequest{}
	err := a.json.Unmarshal(ctx.PostBody(), &reqs)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(ctx, 402, msg)
		return
	}

	for i, r := range reqs {
		reqs[i].Key = a.getModifiedStateKey(r.Key)
	}

	err = a.stateStores[storeName].BulkSet(reqs)
	if err != nil {
		msg := NewErrorResponse("ERR_STATE_SAVE", err.Error())
		respondWithError(ctx, 500, msg)
		return
	}

	respondEmpty(ctx, 201)
}

func (a *api) getModifiedStateKey(key string) string {
	if a.id != "" {
		return fmt.Sprintf("%s%s%s", a.id, daprSeparator, key)
	}

	return key
}

func (a *api) setHeaders(ctx *fasthttp.RequestCtx, metadata map[string]string) {
	headers := []string{}
	ctx.Request.Header.VisitAll(func(key, value []byte) {
		k := string(key)
		v := string(value)

		headers = append(headers, fmt.Sprintf("%s&__header_equals__&%s", k, v))
	})
	if len(headers) > 0 {
		metadata["headers"] = strings.Join(headers, "&__header_delim__&")
	}
}

func (a *api) onDirectMessage(ctx *fasthttp.RequestCtx) {
	targetID := ctx.UserValue(idParam).(string)
	verb := strings.ToUpper(string(ctx.Method()))
	invokeMethodName := ctx.UserValue(methodParam).(string)
	if invokeMethodName == "" {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", "invalid method name")
		respondWithError(ctx, fhttp.StatusBadRequest, msg)
		return
	}

	// Construct internal invoke method request
	req := invokev1.NewInvokeMethodRequest(invokeMethodName).WithHTTPExtension(verb, ctx.QueryArgs().String())
	req.WithRawData(ctx.Request.Body(), string(ctx.Request.Header.ContentType()))
	// Save headers to metadata
	metadata := map[string][]string{}
	ctx.Request.Header.VisitAll(func(key []byte, value []byte) {
		metadata[string(key)] = []string{string(value)}
	})
	req.WithMetadata(metadata)

	resp, err := a.directMessaging.Invoke(targetID, req)
	// err does not represent user application response
	if err != nil {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", err.Error())
		respondWithError(ctx, fhttp.StatusInternalServerError, msg)
		return
	}

	// TODO: add trace parent and state
	invokev1.InternalMetadataToHTTPHeader(resp.Headers(), ctx.Response.Header.Set)
	contentType, body := resp.RawData()
	ctx.Response.Header.SetContentType(contentType)

	// Construct response
	statusCode := int(resp.Status().Code)
	if !resp.IsHTTPResponse() {
		statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
	}
	respond(ctx, statusCode, body)
}

func (a *api) onCreateActorReminder(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(ctx, 400, msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	name := ctx.UserValue(nameParam).(string)

	var req actors.CreateReminderRequest
	err := a.json.Unmarshal(ctx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(ctx, 400, msg)
		return
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.CreateReminder(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_CREATE", err.Error())
		respondWithError(ctx, 500, msg)
	} else {
		respondEmpty(ctx, 200)
	}
}

func (a *api) onCreateActorTimer(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(ctx, 400, msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	name := ctx.UserValue(nameParam).(string)

	var req actors.CreateTimerRequest
	err := a.json.Unmarshal(ctx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(ctx, 400, msg)
		return
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.CreateTimer(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_TIMER_CREATE", err.Error())
		respondWithError(ctx, 500, msg)
	} else {
		respondEmpty(ctx, 200)
	}
}

func (a *api) onDeleteActorReminder(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(ctx, 400, msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	name := ctx.UserValue(nameParam).(string)

	req := actors.DeleteReminderRequest{
		Name:      name,
		ActorID:   actorID,
		ActorType: actorType,
	}

	err := a.actor.DeleteReminder(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_DELETE", err.Error())
		respondWithError(ctx, 500, msg)
	} else {
		respondEmpty(ctx, 200)
	}
}

func (a *api) onActorStateTransaction(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(ctx, 400, msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	body := ctx.PostBody()

	hosted := a.actor.IsActorHosted(&actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", "")
		respondWithError(ctx, 400, msg)
		return
	}

	var ops []actors.TransactionalOperation
	err := a.json.Unmarshal(body, &ops)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(ctx, 400, msg)
		return
	}

	req := actors.TransactionalRequest{
		ActorID:    actorID,
		ActorType:  actorType,
		Operations: ops,
	}

	err = a.actor.TransactionalStateOperation(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_TRANSACTION_SAVE", err.Error())
		respondWithError(ctx, 500, msg)
	} else {
		respondEmpty(ctx, 201)
	}
}

func (a *api) onGetActorReminder(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(ctx, 400, msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	name := ctx.UserValue(nameParam).(string)

	resp, err := a.actor.GetReminder(&actors.GetReminderRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Name:      name,
	})
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", err.Error())
		respondWithError(ctx, 500, msg)
	}
	b, err := a.json.Marshal(resp)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", err.Error())
		respondWithError(ctx, 500, msg)
	} else {
		respondWithJSON(ctx, 200, b)
	}
}

func (a *api) onDeleteActorTimer(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(ctx, 400, msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	name := ctx.UserValue(nameParam).(string)

	req := actors.DeleteTimerRequest{
		Name:      name,
		ActorID:   actorID,
		ActorType: actorType,
	}

	err := a.actor.DeleteTimer(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_TIMER_DELETE", err.Error())
		respondWithError(ctx, 500, msg)
	} else {
		respondEmpty(ctx, 200)
	}
}

func (a *api) onDirectActorMessage(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(ctx, 400, msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	method := ctx.UserValue(methodParam).(string)
	body := ctx.PostBody()

	req := actors.CallRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Method:    method,
		Metadata:  map[string]string{},
		Data:      body,
	}
	a.setHeaders(ctx, req.Metadata)

	resp, err := a.actor.Call(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", err.Error())
		respondWithError(ctx, 500, msg)
	} else {
		statusCode := GetStatusCodeFromMetadata(resp.Metadata)
		a.setHeadersOnRequest(resp.Metadata, ctx)
		respondWithJSON(ctx, statusCode, resp.Data)
	}
}

func (a *api) setHeadersOnRequest(metadata map[string]string, ctx *fasthttp.RequestCtx) {
	if metadata == nil {
		return
	}

	if val, ok := metadata["headers"]; ok {
		headers := strings.Split(val, "&__header_delim__&")
		for _, h := range headers {
			kv := strings.Split(h, "&__header_equals__&")
			ctx.Response.Header.Set(kv[0], kv[1])
		}
	}
}

func (a *api) onSaveActorState(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(ctx, 400, msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	key := ctx.UserValue(stateKeyParam).(string)
	body := ctx.PostBody()

	hosted := a.actor.IsActorHosted(&actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", "")
		respondWithError(ctx, 400, msg)
		return
	}

	// Deserialize body to validate JSON compatible body
	// and remove useless characters before saving
	var val interface{}
	err := a.json.Unmarshal(body, &val)
	if err != nil {
		msg := NewErrorResponse("ERR_DESERIALIZE_HTTP_BODY", err.Error())
		respondWithError(ctx, 400, msg)
		return
	}

	req := actors.SaveStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       key,
		Value:     val,
	}

	err = a.actor.SaveState(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_SAVE", err.Error())
		respondWithError(ctx, 500, msg)
	} else {
		respondEmpty(ctx, 201)
	}
}

func (a *api) onGetActorState(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(ctx, 400, msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	key := ctx.UserValue(stateKeyParam).(string)

	req := actors.GetStateRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Key:       key,
	}

	resp, err := a.actor.GetState(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_GET", err.Error())
		respondWithError(ctx, 500, msg)
	} else {
		respondWithJSON(ctx, 200, resp.Data)
	}
}

func (a *api) onDeleteActorState(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(ctx, 400, msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	key := ctx.UserValue(stateKeyParam).(string)

	hosted := a.actor.IsActorHosted(&actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", "")
		respondWithError(ctx, 400, msg)
		return
	}

	req := actors.DeleteStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       key,
	}

	err := a.actor.DeleteState(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_DELETE", err.Error())
		respondWithError(ctx, 500, msg)
	} else {
		respondEmpty(ctx, 200)
	}
}

func (a *api) onGetMetadata(ctx *fasthttp.RequestCtx) {
	temp := make(map[interface{}]interface{})

	// Copy synchronously so it can be serialized to JSON.
	a.extendedMetadata.Range(func(key, value interface{}) bool {
		temp[key] = value
		return true
	})

	mtd := metadata{
		ID:                a.id,
		ActiveActorsCount: a.actor.GetActiveActorsCount(),
		Extended:          temp,
	}

	mtdBytes, err := a.json.Marshal(mtd)
	if err != nil {
		msg := NewErrorResponse("ERR_METADATA_GET", err.Error())
		respondWithError(ctx, 500, msg)
	} else {
		respondWithJSON(ctx, 200, mtdBytes)
	}
}

func (a *api) onPutMetadata(ctx *fasthttp.RequestCtx) {
	key := ctx.UserValue("key")
	body := ctx.PostBody()
	a.extendedMetadata.Store(key, string(body))
	respondEmpty(ctx, 200)
}

func (a *api) onPublish(ctx *fasthttp.RequestCtx) {
	if a.publishFn == nil {
		msg := NewErrorResponse("ERR_PUBSUB_NOT_FOUND", "")
		respondWithError(ctx, 400, msg)
		return
	}

	topic := ctx.UserValue(topicParam).(string)
	body := ctx.PostBody()
	corID := ctx.Request.Header.Peek(tracing.CorrelationID)
	envelope := pubsub.NewCloudEventsEnvelope(uuid.New().String(), a.id, pubsub.DefaultCloudEventType, string(corID), body)

	b, err := a.json.Marshal(envelope)
	if err != nil {
		msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER", err.Error())
		respondWithError(ctx, 500, msg)
		return
	}

	req := pubsub.PublishRequest{
		Topic: topic,
		Data:  b,
	}
	err = a.publishFn(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_PUBSUB_PUBLISH_MESSAGE", err.Error())
		respondWithError(ctx, 500, msg)
	} else {
		respondEmpty(ctx, 200)
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

func (a *api) onGetHealthz(ctx *fasthttp.RequestCtx) {
	if !a.readyStatus {
		msg := NewErrorResponse("ERR_HEALTH_NOT_READY", "dapr is not ready")
		respondWithError(ctx, 500, msg)
	} else {
		respondEmpty(ctx, 200)
	}
}
