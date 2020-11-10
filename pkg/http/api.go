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

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/channel/http"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/google/uuid"
	jsoniter "github.com/json-iterator/go"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// API returns a list of HTTP endpoints for Dapr
type API interface {
	APIEndpoints() []Endpoint
	MarkStatusAsReady()
	SetAppChannel(appChannel channel.AppChannel)
	SetDirectMessaging(directMessaging messaging.DirectMessaging)
	SetActorRuntime(actor actors.Actors)
}

type api struct {
	endpoints             []Endpoint
	directMessaging       messaging.DirectMessaging
	appChannel            channel.AppChannel
	stateStores           map[string]state.Store
	secretStores          map[string]secretstores.SecretStore
	secretsConfiguration  map[string]config.SecretsScope
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
	concurrencyParam     = "concurrency"
	daprSeparator        = "||"
	pubsubnameparam      = "pubsubname"
	traceparentHeader    = "traceparent"
	tracestateHeader     = "tracestate"
)

// NewAPI returns a new API
func NewAPI(
	appID string,
	appChannel channel.AppChannel,
	directMessaging messaging.DirectMessaging,
	stateStores map[string]state.Store,
	secretStores map[string]secretstores.SecretStore,
	secretsConfiguration map[string]config.SecretsScope,
	publishFn func(*pubsub.PublishRequest) error,
	actor actors.Actors,
	sendToOutputBindingFn func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error),
	tracingSpec config.TracingSpec) API {
	api := &api{
		appChannel:            appChannel,
		directMessaging:       directMessaging,
		stateStores:           stateStores,
		secretStores:          secretStores,
		secretsConfiguration:  secretsConfiguration,
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
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
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
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "state/{storeName}/bulk",
			Version: apiVersionV1,
			Handler: a.onBulkGetState,
		},
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "state/{storeName}/transaction",
			Version: apiVersionV1,
			Handler: a.onPostStateTransaction,
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
			Route:   "publish/{pubsubname}/{topic:*}",
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
			Methods: []string{fasthttp.MethodGet},
			Route:   "actors/{actorType}/{actorId}/state/{key}",
			Version: apiVersionV1,
			Handler: a.onGetActorState,
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
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return
	}

	b, err := a.json.Marshal(req.Data)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST_DATA", fmt.Sprintf(messages.ErrMalformedRequestData, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}

	// pass the trace context to output binding in metadata
	if span := diag_utils.SpanFromContext(reqCtx); span != nil {
		sc := span.SpanContext()
		if req.Metadata == nil {
			req.Metadata = map[string]string{}
		}
		req.Metadata[traceparentHeader] = diag.SpanContextToW3CString(sc)
		if sc.Tracestate != nil {
			req.Metadata[tracestateHeader] = diag.TraceStateToW3CString(sc)
		}
	}

	resp, err := a.sendToOutputBindingFn(name, &bindings.InvokeRequest{
		Metadata:  req.Metadata,
		Data:      b,
		Operation: bindings.OperationKind(req.Operation),
	})
	if err != nil {
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", fmt.Sprintf(messages.ErrInvokeOutputBinding, name, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}
	if resp == nil {
		respondEmpty(reqCtx)
	} else {
		respondWithJSON(reqCtx, fasthttp.StatusOK, resp.Data)
	}
}

func (a *api) onBulkGetState(reqCtx *fasthttp.RequestCtx) {
	store, err := a.getStateStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	var req BulkGetRequest
	err = a.json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return
	}

	metadata := getMetadataFromRequest(reqCtx)

	bulkResp := make([]BulkGetResponse, len(req.Keys))
	limiter := concurrency.NewLimiter(req.Parallelism)

	for i, k := range req.Keys {
		bulkResp[i].Key = k
		fn := func(param interface{}) {
			r := param.(*BulkGetResponse)
			gr := &state.GetRequest{
				Key:      a.getModifiedStateKey(r.Key),
				Metadata: metadata,
			}

			resp, err := store.Get(gr)
			if err != nil {
				log.Debugf("bulk get: error getting key %s: %s", r.Key, err)
				r.Error = err.Error()
			} else if resp != nil {
				r.Data = jsoniter.RawMessage(resp.Data)
				r.ETag = resp.ETag
			}
		}

		limiter.Execute(fn, &bulkResp[i])
	}
	limiter.Wait()

	b, _ := a.json.Marshal(bulkResp)
	respondWithJSON(reqCtx, fasthttp.StatusOK, b)
}

func (a *api) getStateStoreWithRequestValidation(reqCtx *fasthttp.RequestCtx) (state.Store, error) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORES_NOT_CONFIGURED", messages.ErrStateStoresNotConfigured)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return nil, errors.New(msg.Message)
	}

	storeName := a.getStateStoreName(reqCtx)

	if a.stateStores[storeName] == nil {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrStateStoreNotFound, storeName))
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return nil, errors.New(msg.Message)
	}
	return a.stateStores[storeName], nil
}

func (a *api) onGetState(reqCtx *fasthttp.RequestCtx) {
	store, err := a.getStateStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
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

	resp, err := store.Get(&req)
	if err != nil {
		storeName := a.getStateStoreName(reqCtx)
		msg := NewErrorResponse("ERR_STATE_GET", fmt.Sprintf(messages.ErrStateGet, key, storeName, err.Error()))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}
	if resp == nil || resp.Data == nil {
		respondEmpty(reqCtx)
		return
	}
	respondWithETaggedJSON(reqCtx, fasthttp.StatusOK, resp.Data, resp.ETag)
}

func (a *api) onDeleteState(reqCtx *fasthttp.RequestCtx) {
	store, err := a.getStateStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	key := reqCtx.UserValue(stateKeyParam).(string)
	etag := string(reqCtx.Request.Header.Peek("If-Match"))

	concurrency := string(reqCtx.QueryArgs().Peek(concurrencyParam))
	consistency := string(reqCtx.QueryArgs().Peek(consistencyParam))

	metadata := getMetadataFromRequest(reqCtx)

	req := state.DeleteRequest{
		Key:  a.getModifiedStateKey(key),
		ETag: etag,
		Options: state.DeleteStateOption{
			Concurrency: concurrency,
			Consistency: consistency,
		},
		Metadata: metadata,
	}

	err = store.Delete(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_STATE_DELETE", fmt.Sprintf(messages.ErrStateDelete, key, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}
	respondEmpty(reqCtx)
}

func (a *api) onGetSecret(reqCtx *fasthttp.RequestCtx) {
	if a.secretStores == nil || len(a.secretStores) == 0 {
		msg := NewErrorResponse("ERR_SECRET_STORES_NOT_CONFIGURED", messages.ErrSecretStoreNotConfigured)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}

	secretStoreName := reqCtx.UserValue(secretStoreNameParam).(string)

	if a.secretStores[secretStoreName] == nil {
		msg := NewErrorResponse("ERR_SECRET_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrSecretStoreNotFound, secretStoreName))
		respondWithError(reqCtx, fasthttp.StatusUnauthorized, msg)
		log.Debug(msg)
		return
	}

	metadata := getMetadataFromRequest(reqCtx)

	key := reqCtx.UserValue(secretNameParam).(string)

	if !a.isSecretAllowed(secretStoreName, key) {
		msg := NewErrorResponse("ERR_PERMISSION_DENIED", fmt.Sprintf(messages.ErrPermissionDenied, key, secretStoreName))
		respondWithError(reqCtx, fasthttp.StatusForbidden, msg)
		return
	}

	req := secretstores.GetSecretRequest{
		Name:     key,
		Metadata: metadata,
	}

	resp, err := a.secretStores[secretStoreName].GetSecret(req)
	if err != nil {
		msg := NewErrorResponse("ERR_SECRET_GET",
			fmt.Sprintf(messages.ErrSecretGet, req.Name, secretStoreName, err.Error()))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}

	if resp.Data == nil {
		respondEmpty(reqCtx)
		return
	}

	respBytes, _ := a.json.Marshal(resp.Data)
	respondWithJSON(reqCtx, fasthttp.StatusOK, respBytes)
}

func (a *api) onPostState(reqCtx *fasthttp.RequestCtx) {
	store, err := a.getStateStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	reqs := []state.SetRequest{}
	err = a.json.Unmarshal(reqCtx.PostBody(), &reqs)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return
	}

	for i, r := range reqs {
		reqs[i].Key = a.getModifiedStateKey(r.Key)
	}

	err = store.BulkSet(reqs)
	if err != nil {
		storeName := a.getStateStoreName(reqCtx)
		msg := NewErrorResponse("ERR_STATE_SAVE", fmt.Sprintf(messages.ErrStateSave, storeName, err.Error()))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}

	respondEmpty(reqCtx)
}

func (a *api) getStateStoreName(reqCtx *fasthttp.RequestCtx) string {
	return reqCtx.UserValue(storeNameParam).(string)
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
	// Router gives "/" if method parameter is empty
	if invokeMethodName == "/" {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", messages.ErrDirectInvokeMethod)
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return
	}

	if a.directMessaging == nil {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", messages.ErrDirectInvokeNotReady)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
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
		// Allowlists policies that are applied on the callee side can return a Permission Denied error.
		// For everything else, treat it as a gRPC transport error
		statusCode := fasthttp.StatusInternalServerError
		if status.Code(err) == codes.PermissionDenied {
			statusCode = invokev1.HTTPStatusFromCode(codes.PermissionDenied)
		}
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", fmt.Sprintf(messages.ErrDirectInvoke, targetID, err))
		respondWithError(reqCtx, statusCode, msg)
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
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	var req actors.CreateReminderRequest
	err := a.json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.CreateReminder(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_CREATE", fmt.Sprintf(messages.ErrActorReminderCreate, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
	} else {
		respondEmpty(reqCtx)
	}
}

func (a *api) onCreateActorTimer(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	var req actors.CreateTimerRequest
	err := a.json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.CreateTimer(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_TIMER_CREATE", fmt.Sprintf(messages.ErrActorTimerCreate, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
	} else {
		respondEmpty(reqCtx)
	}
}

func (a *api) onDeleteActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
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
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_DELETE", fmt.Sprintf(messages.ErrActorReminderDelete, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
	} else {
		respondEmpty(reqCtx)
	}
}

func (a *api) onActorStateTransaction(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	body := reqCtx.PostBody()

	var ops []actors.TransactionalOperation
	err := a.json.Unmarshal(body, &ops)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return
	}

	hosted := a.actor.IsActorHosted(reqCtx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", messages.ErrActorInstanceMissing)
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return
	}

	req := actors.TransactionalRequest{
		ActorID:    actorID,
		ActorType:  actorType,
		Operations: ops,
	}

	err = a.actor.TransactionalStateOperation(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_TRANSACTION_SAVE", fmt.Sprintf(messages.ErrActorStateTransactionSave, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
	} else {
		respondEmpty(reqCtx)
	}
}

func (a *api) onGetActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
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
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", fmt.Sprintf(messages.ErrActorReminderGet, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}
	b, err := a.json.Marshal(resp)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", fmt.Sprintf(messages.ErrActorReminderGet, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}

	respondWithJSON(reqCtx, fasthttp.StatusOK, b)
}

func (a *api) onDeleteActorTimer(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
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
		msg := NewErrorResponse("ERR_ACTOR_TIMER_DELETE", fmt.Sprintf(messages.ErrActorTimerDelete, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
	} else {
		respondEmpty(reqCtx)
	}
}

func (a *api) onDirectActorMessage(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
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
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
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

func (a *api) onGetActorState(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
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
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", messages.ErrActorInstanceMissing)
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return
	}

	req := actors.GetStateRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Key:       key,
	}

	resp, err := a.actor.GetState(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_GET", fmt.Sprintf(messages.ErrActorStateGet, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
	} else {
		if resp == nil || resp.Data == nil {
			respondEmpty(reqCtx)
			return
		}
		respondWithJSON(reqCtx, fasthttp.StatusOK, resp.Data)
	}
}

func (a *api) onGetMetadata(reqCtx *fasthttp.RequestCtx) {
	temp := make(map[interface{}]interface{})

	// Copy synchronously so it can be serialized to JSON.
	a.extendedMetadata.Range(func(key, value interface{}) bool {
		temp[key] = value
		return true
	})

	activeActorsCount := []actors.ActiveActorsCount{}
	if a.actor != nil {
		activeActorsCount = a.actor.GetActiveActorsCount(reqCtx)
	}

	mtd := metadata{
		ID:                a.id,
		ActiveActorsCount: activeActorsCount,
		Extended:          temp,
	}

	mtdBytes, err := a.json.Marshal(mtd)
	if err != nil {
		msg := NewErrorResponse("ERR_METADATA_GET", fmt.Sprintf(messages.ErrMetadataGet, err))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
	} else {
		respondWithJSON(reqCtx, fasthttp.StatusOK, mtdBytes)
	}
}

func (a *api) onPutMetadata(reqCtx *fasthttp.RequestCtx) {
	key := fmt.Sprintf("%v", reqCtx.UserValue("key"))
	body := reqCtx.PostBody()
	a.extendedMetadata.Store(key, string(body))
	respondEmpty(reqCtx)
}

func (a *api) onPublish(reqCtx *fasthttp.RequestCtx) {
	if a.publishFn == nil {
		msg := NewErrorResponse("ERR_PUBSUB_NOT_FOUND", messages.ErrPubsubNotFound)
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return
	}

	pubsubName := reqCtx.UserValue(pubsubnameparam).(string)
	if pubsubName == "" {
		msg := NewErrorResponse("ERR_PUBSUB_EMPTY", messages.ErrPubsubEmpty)
		respondWithError(reqCtx, fasthttp.StatusNotFound, msg)
		log.Debug(msg)
		return
	}

	topic := reqCtx.UserValue(topicParam).(string)
	// FIXME: isn't it "" instead?
	if topic == "/" {
		msg := NewErrorResponse("ERR_TOPIC_EMPTY", fmt.Sprintf(messages.ErrTopicEmpty, pubsubName))
		respondWithError(reqCtx, fasthttp.StatusNotFound, msg)
		log.Debug(msg)
		return
	}

	body := reqCtx.PostBody()

	// Extract trace context from context.
	span := diag_utils.SpanFromContext(reqCtx)
	// Populate W3C traceparent to cloudevent envelope
	corID := diag.SpanContextToW3CString(span.SpanContext())
	envelope := pubsub.NewCloudEventsEnvelope(uuid.New().String(), a.id, pubsub.DefaultCloudEventType, corID, topic, pubsubName, body)

	b, err := a.json.Marshal(envelope)
	if err != nil {
		msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER",
			fmt.Sprintf(messages.ErrPubsubCloudEventsSer, topic, pubsubName, err.Error()))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}

	req := pubsub.PublishRequest{
		PubsubName: pubsubName,
		Topic:      topic,
		Data:       b,
	}

	err = a.publishFn(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_PUBSUB_PUBLISH_MESSAGE",
			fmt.Sprintf(messages.ErrPubsubPublishMessage, topic, pubsubName, err.Error()))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
	} else {
		respondEmpty(reqCtx)
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
	return fasthttp.StatusOK
}

func (a *api) onGetHealthz(reqCtx *fasthttp.RequestCtx) {
	if !a.readyStatus {
		msg := NewErrorResponse("ERR_HEALTH_NOT_READY", messages.ErrHealthNotReady)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
	} else {
		respondEmpty(reqCtx)
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

func (a *api) onPostStateTransaction(reqCtx *fasthttp.RequestCtx) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORES_NOT_CONFIGURED", messages.ErrStateStoresNotConfigured)
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}

	storeName := reqCtx.UserValue(storeNameParam).(string)
	stateStore, ok := a.stateStores[storeName]
	if !ok {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrStateStoreNotFound, storeName))
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return
	}

	transactionalStore, ok := stateStore.(state.TransactionalStore)
	if !ok {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_SUPPORTED", fmt.Sprintf(messages.ErrStateStoreNotSupported, storeName))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}

	body := reqCtx.PostBody()
	var req state.TransactionalStateRequest
	if err := a.json.Unmarshal(body, &req); err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err.Error()))
		respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
		log.Debug(msg)
		return
	}

	operations := []state.TransactionalStateOperation{}
	for _, o := range req.Operations {
		switch o.Operation {
		case state.Upsert:
			var upsertReq state.SetRequest
			if err := mapstructure.Decode(o.Request, &upsertReq); err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST",
					fmt.Sprintf(messages.ErrMalformedRequest, err.Error()))
				respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
				log.Debug(msg)
				return
			}
			upsertReq.Key = a.getModifiedStateKey(upsertReq.Key)
			operations = append(operations, state.TransactionalStateOperation{
				Request:   upsertReq,
				Operation: state.Upsert,
			})
		case state.Delete:
			var delReq state.DeleteRequest
			if err := mapstructure.Decode(o.Request, &delReq); err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST",
					fmt.Sprintf(messages.ErrMalformedRequest, err.Error()))
				respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
				log.Debug(msg)
				return
			}
			delReq.Key = a.getModifiedStateKey(delReq.Key)
			operations = append(operations, state.TransactionalStateOperation{
				Request:   delReq,
				Operation: state.Delete,
			})
		default:
			msg := NewErrorResponse(
				"ERR_NOT_SUPPORTED_STATE_OPERATION",
				fmt.Sprintf(messages.ErrNotSupportedStateOperation, o.Operation))
			respondWithError(reqCtx, fasthttp.StatusBadRequest, msg)
			log.Debug(msg)
			return
		}
	}

	err := transactionalStore.Multi(&state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   req.Metadata,
	})

	if err != nil {
		msg := NewErrorResponse("ERR_STATE_TRANSACTION", fmt.Sprintf(messages.ErrStateTransaction, err.Error()))
		respondWithError(reqCtx, fasthttp.StatusInternalServerError, msg)
		log.Debug(msg)
	} else {
		respondEmpty(reqCtx)
	}
}

func (a *api) isSecretAllowed(storeName, key string) bool {
	if config, ok := a.secretsConfiguration[storeName]; ok {
		return config.IsSecretAllowed(key)
	}
	// By default if a configuration is not defined for a secret store, return true.
	return true
}

func (a *api) SetAppChannel(appChannel channel.AppChannel) {
	a.appChannel = appChannel
}

func (a *api) SetDirectMessaging(directMessaging messaging.DirectMessaging) {
	a.directMessaging = directMessaging
}

func (a *api) SetActorRuntime(actor actors.Actors) {
	a.actor = actor
}
