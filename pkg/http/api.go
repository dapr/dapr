/*
Copyright 2021 The Dapr Authors
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
	"encoding/base64"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/components-contrib/lock"
	lock_loader "github.com/dapr/dapr/pkg/components/lock"
	"github.com/dapr/dapr/pkg/version"

	contrib_metadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/channel/http"
	state_loader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	runtime_pubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

// API returns a list of HTTP endpoints for Dapr.
type API interface {
	APIEndpoints() []Endpoint
	PublicEndpoints() []Endpoint
	MarkStatusAsReady()
	MarkStatusAsOutboundReady()
	SetAppChannel(appChannel channel.AppChannel)
	SetDirectMessaging(directMessaging messaging.DirectMessaging)
	SetActorRuntime(actor actors.Actors)
}

type api struct {
	endpoints                  []Endpoint
	publicEndpoints            []Endpoint
	directMessaging            messaging.DirectMessaging
	appChannel                 channel.AppChannel
	getComponentsFn            func() []components_v1alpha1.Component
	resiliency                 resiliency.Provider
	stateStores                map[string]state.Store
	lockStores                 map[string]lock.Store
	configurationStores        map[string]configuration.Store
	configurationSubscribe     map[string]chan struct{}
	transactionalStateStores   map[string]state.TransactionalStore
	secretStores               map[string]secretstores.SecretStore
	secretsConfiguration       map[string]config.SecretsScope
	actor                      actors.Actors
	pubsubAdapter              runtime_pubsub.Adapter
	sendToOutputBindingFn      func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	id                         string
	extendedMetadata           sync.Map
	readyStatus                bool
	outboundReadyStatus        bool
	tracingSpec                config.TracingSpec
	shutdown                   func()
	getComponentsCapabilitesFn func() map[string][]string
	daprRunTimeVersion         string
}

type registeredComponent struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

type metadata struct {
	ID                   string                     `json:"id"`
	ActiveActorsCount    []actors.ActiveActorsCount `json:"actors"`
	Extended             map[string]string          `json:"extended"`
	RegisteredComponents []registeredComponent      `json:"components"`
}

const (
	apiVersionV1             = "v1.0"
	apiVersionV1alpha1       = "v1.0-alpha1"
	idParam                  = "id"
	methodParam              = "method"
	topicParam               = "topic"
	actorTypeParam           = "actorType"
	actorIDParam             = "actorId"
	storeNameParam           = "storeName"
	stateKeyParam            = "key"
	configurationKeyParam    = "key"
	configurationSubscribeID = "configurationSubscribeID"
	secretStoreNameParam     = "secretStoreName"
	secretNameParam          = "key"
	nameParam                = "name"
	consistencyParam         = "consistency"
	concurrencyParam         = "concurrency"
	pubsubnameparam          = "pubsubname"
	traceparentHeader        = "traceparent"
	tracestateHeader         = "tracestate"
	daprAppID                = "dapr-app-id"
	daprRuntimeVersionKey    = "daprRuntimeVersion"
)

// NewAPI returns a new API.
func NewAPI(
	appID string,
	appChannel channel.AppChannel,
	directMessaging messaging.DirectMessaging,
	getComponentsFn func() []components_v1alpha1.Component,
	resiliency resiliency.Provider,
	stateStores map[string]state.Store,
	lockStores map[string]lock.Store,
	secretStores map[string]secretstores.SecretStore,
	secretsConfiguration map[string]config.SecretsScope,
	configurationStores map[string]configuration.Store,
	pubsubAdapter runtime_pubsub.Adapter,
	actor actors.Actors,
	sendToOutputBindingFn func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error),
	tracingSpec config.TracingSpec,
	shutdown func(),
	getComponentsCapabilitiesFn func() map[string][]string,
) API {
	transactionalStateStores := map[string]state.TransactionalStore{}
	for key, store := range stateStores {
		if state.FeatureTransactional.IsPresent(store.Features()) {
			transactionalStateStores[key] = store.(state.TransactionalStore)
		}
	}
	api := &api{
		appChannel:                 appChannel,
		getComponentsFn:            getComponentsFn,
		resiliency:                 resiliency,
		directMessaging:            directMessaging,
		stateStores:                stateStores,
		lockStores:                 lockStores,
		transactionalStateStores:   transactionalStateStores,
		secretStores:               secretStores,
		secretsConfiguration:       secretsConfiguration,
		configurationStores:        configurationStores,
		configurationSubscribe:     make(map[string]chan struct{}),
		actor:                      actor,
		pubsubAdapter:              pubsubAdapter,
		sendToOutputBindingFn:      sendToOutputBindingFn,
		id:                         appID,
		tracingSpec:                tracingSpec,
		shutdown:                   shutdown,
		getComponentsCapabilitesFn: getComponentsCapabilitiesFn,
		daprRunTimeVersion:         version.Version(),
	}

	metadataEndpoints := api.constructMetadataEndpoints()
	healthEndpoints := api.constructHealthzEndpoints()

	api.endpoints = append(api.endpoints, api.constructStateEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructSecretEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructPubSubEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructActorEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructDirectMessagingEndpoints()...)
	api.endpoints = append(api.endpoints, metadataEndpoints...)
	api.endpoints = append(api.endpoints, api.constructShutdownEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructBindingsEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructConfigurationEndpoints()...)
	api.endpoints = append(api.endpoints, healthEndpoints...)
	api.endpoints = append(api.endpoints, api.constructDistributedLockEndpoints()...)

	api.publicEndpoints = append(api.publicEndpoints, metadataEndpoints...)
	api.publicEndpoints = append(api.publicEndpoints, healthEndpoints...)

	return api
}

// APIEndpoints returns the list of registered endpoints.
func (a *api) APIEndpoints() []Endpoint {
	return a.endpoints
}

// PublicEndpoints returns the list of registered endpoints.
func (a *api) PublicEndpoints() []Endpoint {
	return a.publicEndpoints
}

// MarkStatusAsReady marks the ready status of dapr.
func (a *api) MarkStatusAsReady() {
	a.readyStatus = true
}

// MarkStatusAsOutboundReady marks the ready status of dapr for outbound traffic.
func (a *api) MarkStatusAsOutboundReady() {
	a.outboundReadyStatus = true
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
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "state/{storeName}/query",
			Version: apiVersionV1alpha1,
			Handler: a.onQueryState,
		},
	}
}

func (a *api) constructSecretEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "secrets/{secretStoreName}/bulk",
			Version: apiVersionV1,
			Handler: a.onBulkGetSecret,
		},
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
			Methods:           []string{router.MethodWild},
			Route:             "invoke/{id}/method/{method:*}",
			Alias:             "{method:*}",
			Version:           apiVersionV1,
			KeepParamUnescape: true,
			Handler:           a.onDirectMessage,
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
		{
			Methods: []string{fasthttp.MethodPatch},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Handler: a.onRenameActorReminder,
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

func (a *api) constructShutdownEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodPost},
			Route:   "shutdown",
			Version: apiVersionV1,
			Handler: a.onShutdown,
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
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "healthz/outbound",
			Version: apiVersionV1,
			Handler: a.onGetOutboundHealthz,
		},
	}
}

func (a *api) constructConfigurationEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "configuration/{storeName}",
			Version: apiVersionV1alpha1,
			Handler: a.onGetConfiguration,
		},
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "configuration/{storeName}/subscribe",
			Version: apiVersionV1alpha1,
			Handler: a.onSubscribeConfiguration,
		},
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "configuration/{storeName}/{configurationSubscribeID}/unsubscribe",
			Version: apiVersionV1alpha1,
			Handler: a.onUnsubscribeConfiguration,
		},
	}
}

func (a *api) constructDistributedLockEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodPost},
			Route:   "lock/{storeName}",
			Version: apiVersionV1alpha1,
			Handler: a.onLock,
		},
		{
			Methods: []string{fasthttp.MethodPost},
			Route:   "unlock/{storeName}",
			Version: apiVersionV1alpha1,
			Handler: a.onUnlock,
		},
	}
}

func (a *api) onOutputBindingMessage(reqCtx *fasthttp.RequestCtx) {
	name := reqCtx.UserValue(nameParam).(string)
	body := reqCtx.PostBody()

	var req OutputBindingRequest
	err := json.Unmarshal(body, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	b, err := json.Marshal(req.Data)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST_DATA", fmt.Sprintf(messages.ErrMalformedRequestData, err))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	// pass the trace context to output binding in metadata
	if span := diag_utils.SpanFromContext(reqCtx); span != nil {
		sc := span.SpanContext()
		if req.Metadata == nil {
			req.Metadata = map[string]string{}
		}
		// if sc is not empty context, set traceparent Header.
		if !sc.Equal(trace.SpanContext{}) {
			req.Metadata[traceparentHeader] = diag.SpanContextToW3CString(sc)
		}
		if sc.TraceState().Len() == 0 {
			req.Metadata[tracestateHeader] = diag.TraceStateToW3CString(sc)
		}
	}

	start := time.Now()
	resp, err := a.sendToOutputBindingFn(name, &bindings.InvokeRequest{
		Metadata:  req.Metadata,
		Data:      b,
		Operation: bindings.OperationKind(req.Operation),
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.OutputBindingEvent(context.Background(), name, req.Operation, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", fmt.Sprintf(messages.ErrInvokeOutputBinding, name, err))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if resp == nil {
		respond(reqCtx, withEmpty())
	} else {
		respond(reqCtx, withMetadata(resp.Metadata), withJSON(fasthttp.StatusOK, resp.Data))
	}
}

func (a *api) onBulkGetState(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getStateStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	var req BulkGetRequest
	err = json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	// merge metadata from URL query parameters
	metadata := getMetadataFromRequest(reqCtx)
	if req.Metadata == nil {
		req.Metadata = metadata
	} else {
		for k, v := range metadata {
			req.Metadata[k] = v
		}
	}

	bulkResp := make([]BulkGetResponse, len(req.Keys))
	if len(req.Keys) == 0 {
		b, _ := json.Marshal(bulkResp)
		respond(reqCtx, withJSON(fasthttp.StatusOK, b))
		return
	}

	// try bulk get first
	reqs := make([]state.GetRequest, len(req.Keys))
	for i, k := range req.Keys {
		key, err1 := state_loader.GetModifiedStateKey(k, storeName, a.id)
		if err1 != nil {
			msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err1))
			respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(err1)
			return
		}
		r := state.GetRequest{
			Key:      key,
			Metadata: req.Metadata,
		}
		reqs[i] = r
	}

	start := time.Now()
	var bulkGet bool
	var responses []state.BulkGetResponse
	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, storeName)
	rErr := policy(func(ctx context.Context) (rErr error) {
		bulkGet, responses, rErr = store.BulkGet(reqs)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(context.Background(), storeName, diag.BulkGet, err == nil, elapsed)

	if bulkGet {
		// if store supports bulk get
		if rErr != nil {
			msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
			respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(msg)
			return
		}

		for i := 0; i < len(responses) && i < len(req.Keys); i++ {
			bulkResp[i].Key = state_loader.GetOriginalStateKey(responses[i].Key)
			if responses[i].Error != "" {
				log.Debugf("bulk get: error getting key %s: %s", bulkResp[i].Key, responses[i].Error)
				bulkResp[i].Error = responses[i].Error
			} else {
				bulkResp[i].Data = json.RawMessage(responses[i].Data)
				bulkResp[i].ETag = responses[i].ETag
				bulkResp[i].Metadata = responses[i].Metadata
			}
		}
	} else {
		// if store doesn't support bulk get, fallback to call get() method one by one
		limiter := concurrency.NewLimiter(req.Parallelism)

		for i, k := range req.Keys {
			bulkResp[i].Key = k

			fn := func(param interface{}) {
				r := param.(*BulkGetResponse)
				k, err := state_loader.GetModifiedStateKey(r.Key, storeName, a.id)
				if err != nil {
					log.Debug(err)
					r.Error = err.Error()
					return
				}
				gr := &state.GetRequest{
					Key:      k,
					Metadata: metadata,
				}

				var resp *state.GetResponse
				err = policy(func(ctx context.Context) (rErr error) {
					resp, rErr = store.Get(gr)
					return rErr
				})
				if err != nil {
					log.Debugf("bulk get: error getting key %s: %s", r.Key, err)
					r.Error = err.Error()
				} else if resp != nil {
					r.Data = json.RawMessage(resp.Data)
					r.ETag = resp.ETag
					r.Metadata = resp.Metadata
				}
			}

			limiter.Execute(fn, &bulkResp[i])
		}
		limiter.Wait()
	}

	if encryption.EncryptedStateStore(storeName) {
		for i := range bulkResp {
			val, err := encryption.TryDecryptValue(storeName, bulkResp[i].Data)
			if err != nil {
				log.Debugf("bulk get error: %s", err)
				bulkResp[i].Error = err.Error()
				continue
			}

			bulkResp[i].Data = val
		}
	}

	b, _ := json.Marshal(bulkResp)
	respond(reqCtx, withJSON(fasthttp.StatusOK, b))
}

func (a *api) getStateStoreWithRequestValidation(reqCtx *fasthttp.RequestCtx) (state.Store, string, error) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_CONFIGURED", messages.ErrStateStoresNotConfigured)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}

	storeName := a.getStateStoreName(reqCtx)

	if a.stateStores[storeName] == nil {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrStateStoreNotFound, storeName))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}
	return a.stateStores[storeName], storeName, nil
}

func (a *api) getLockStoreWithRequestValidation(reqCtx *fasthttp.RequestCtx) (lock.Store, string, error) {
	if a.lockStores == nil || len(a.lockStores) == 0 {
		msg := NewErrorResponse("ERR_LOCK_STORE_NOT_CONFIGURED", messages.ErrLockStoresNotConfigured)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}

	storeName := a.getStateStoreName(reqCtx)

	if a.lockStores[storeName] == nil {
		msg := NewErrorResponse("ERR_LOCK_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrLockStoreNotFound, storeName))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}
	return a.lockStores[storeName], storeName, nil
}

func (a *api) onGetState(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getStateStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromRequest(reqCtx)

	key := reqCtx.UserValue(stateKeyParam).(string)
	consistency := string(reqCtx.QueryArgs().Peek(consistencyParam))
	k, err := state_loader.GetModifiedStateKey(key, storeName, a.id)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(err)
		return
	}
	req := state.GetRequest{
		Key: k,
		Options: state.GetStateOption{
			Consistency: consistency,
		},
		Metadata: metadata,
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, storeName)
	var resp *state.GetResponse
	err = policy(func(ctx context.Context) (rErr error) {
		resp, rErr = store.Get(&req)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(context.Background(), storeName, diag.Get, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_STATE_GET", fmt.Sprintf(messages.ErrStateGet, key, storeName, err.Error()))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	if resp == nil || resp.Data == nil {
		respond(reqCtx, withEmpty())
		return
	}

	if encryption.EncryptedStateStore(storeName) {
		val, err := encryption.TryDecryptValue(storeName, resp.Data)
		if err != nil {
			msg := NewErrorResponse("ERR_STATE_GET", fmt.Sprintf(messages.ErrStateGet, key, storeName, err.Error()))
			respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
			log.Debug(msg)
			return
		}

		resp.Data = val
	}

	respond(reqCtx, withJSON(fasthttp.StatusOK, resp.Data), withEtag(resp.ETag), withMetadata(resp.Metadata))
}

func (a *api) getConfigurationStoreWithRequestValidation(reqCtx *fasthttp.RequestCtx) (configuration.Store, string, error) {
	if a.configurationStores == nil || len(a.configurationStores) == 0 {
		msg := NewErrorResponse("ERR_CONFIGURATION_STORE_NOT_CONFIGURED", messages.ErrConfigurationStoresNotConfigured)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}

	storeName := a.getStateStoreName(reqCtx)

	if a.configurationStores[storeName] == nil {
		msg := NewErrorResponse("ERR_CONFIGURATION_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrConfigurationStoreNotFound, storeName))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}
	return a.configurationStores[storeName], storeName, nil
}

type subscribeConfigurationResponse struct {
	ID string `json:"id"`
}

type configurationEventHandler struct {
	api        *api
	storeName  string
	appChannel channel.AppChannel
	res        resiliency.Provider
}

func (h *configurationEventHandler) updateEventHandler(ctx context.Context, e *configuration.UpdateEvent) error {
	for _, item := range e.Items {
		req := invokev1.NewInvokeMethodRequest(fmt.Sprintf("/configuration/%s/%s", h.storeName, item.Key))
		req.WithHTTPExtension(nethttp.MethodPost, "")
		eventBody, _ := json.Marshal(e)
		req.WithRawData(eventBody, invokev1.JSONContentType)

		policy := h.res.ComponentInboundPolicy(ctx, h.storeName)
		err := policy(func(ctx context.Context) (err error) {
			resp, err := h.appChannel.InvokeMethod(ctx, req)
			if err != nil {
				return err
			}

			if resp != nil && resp.Status().Code != nethttp.StatusOK {
				return errors.Errorf("Error sending configuration item to application, status %d", resp.Status().Code)
			}
			return nil
		})
		if err != nil {
			log.Error(errors.Wrap(err, "error sending configuration item to the app"))
		}
	}
	return nil
}

func (a *api) onLock(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getLockStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	req := lock.TryLockRequest{}
	err = json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, storeName)

	var resp *lock.TryLockResponse
	req.ResourceID, err = lock_loader.GetModifiedLockKey(req.ResourceID, storeName, a.id)
	if err != nil {
		msg := NewErrorResponse("ERR_TRY_LOCK", err.Error())
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	err = policy(func(ctx context.Context) (rErr error) {
		resp, rErr = store.TryLock(&req)
		return rErr
	})
	if err != nil {
		msg := NewErrorResponse("ERR_TRY_LOCK", err.Error())
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	b, _ := json.Marshal(resp)
	respond(reqCtx, withJSON(200, b))
}

func (a *api) onUnlock(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getLockStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	req := lock.UnlockRequest{}
	err = json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, storeName)

	var resp *lock.UnlockResponse
	req.ResourceID, err = lock_loader.GetModifiedLockKey(req.ResourceID, storeName, a.id)
	if err != nil {
		msg := NewErrorResponse("ERR_UNLOCK", err.Error())
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	err = policy(func(ctx context.Context) (rErr error) {
		resp, rErr = store.Unlock(&req)
		return rErr
	})
	if err != nil {
		msg := NewErrorResponse("ERR_UNLOCK", err.Error())
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	b, _ := json.Marshal(resp)
	respond(reqCtx, withJSON(200, b))
}

func (a *api) onSubscribeConfiguration(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getConfigurationStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromRequest(reqCtx)
	subscribeKeys := make([]string, 0)

	keys := make([]string, 0)
	queryKeys := reqCtx.QueryArgs().PeekMulti(configurationKeyParam)
	for _, queryKeyByte := range queryKeys {
		keys = append(keys, string(queryKeyByte))
	}

	// empty list means subscribing to all configuration keys
	if len(keys) == 0 {
		getConfigurationReq := &configuration.GetRequest{
			Keys:     []string{},
			Metadata: metadata,
		}

		start := time.Now()
		policy := a.resiliency.ComponentOutboundPolicy(reqCtx, storeName)
		var getResponse *configuration.GetResponse
		err = policy(func(ctx context.Context) (rErr error) {
			getResponse, rErr = store.Get(ctx, getConfigurationReq)
			return rErr
		})
		elapsed := diag.ElapsedSince(start)
		diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), storeName, diag.Get, err == nil, elapsed)

		if err != nil {
			msg := NewErrorResponse("ERR_CONFIGURATION_SUBSCRIBE", fmt.Sprintf(messages.ErrConfigurationSubscribe, keys, storeName, err.Error()))
			respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
			log.Debug(msg)
			return
		}
		items := getResponse.Items
		for _, item := range items {
			subscribeKeys = append(subscribeKeys, item.Key)
		}
	} else {
		subscribeKeys = append(subscribeKeys, keys...)
	}

	req := configuration.SubscribeRequest{
		Keys:     subscribeKeys,
		Metadata: metadata,
	}

	// create handler
	handler := &configurationEventHandler{
		api:        a,
		storeName:  storeName,
		appChannel: a.appChannel,
		res:        a.resiliency,
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, storeName)
	var subscribeID string
	err = policy(func(ctx context.Context) (rErr error) {
		subscribeID, rErr = store.Subscribe(ctx, &req, handler.updateEventHandler)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), storeName, diag.ConfigurationSubscribe, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_CONFIGURATION_SUBSCRIBE", fmt.Sprintf(messages.ErrConfigurationSubscribe, keys, storeName, err.Error()))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	respBytes, _ := json.Marshal(&subscribeConfigurationResponse{
		ID: subscribeID,
	})
	respond(reqCtx, withJSON(fasthttp.StatusOK, respBytes))
}

func (a *api) onUnsubscribeConfiguration(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getConfigurationStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}
	subscribeID := reqCtx.UserValue(configurationSubscribeID).(string)

	req := configuration.UnsubscribeRequest{
		ID: subscribeID,
	}
	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, storeName)
	err = policy(func(ctx context.Context) (rErr error) {
		return store.Unsubscribe(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)
	diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), storeName, diag.ConfigurationUnsubscribe, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_CONFIGURATION_UNSUBSCRIBE", fmt.Sprintf(messages.ErrConfigurationUnsubscribe, subscribeID, err.Error()))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	respond(reqCtx, withEmpty())
}

func (a *api) onGetConfiguration(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getConfigurationStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromRequest(reqCtx)

	keys := make([]string, 0)
	queryKeys := reqCtx.QueryArgs().PeekMulti(configurationKeyParam)
	for _, queryKeyByte := range queryKeys {
		keys = append(keys, string(queryKeyByte))
	}
	req := configuration.GetRequest{
		Keys:     keys,
		Metadata: metadata,
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, storeName)
	var getResponse *configuration.GetResponse
	err = policy(func(ctx context.Context) (rErr error) {
		getResponse, rErr = store.Get(ctx, &req)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), storeName, diag.Get, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_CONFIGURATION_GET", fmt.Sprintf(messages.ErrConfigurationGet, keys, storeName, err.Error()))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	if getResponse == nil || getResponse.Items == nil || len(getResponse.Items) == 0 {
		respond(reqCtx, withEmpty())
		return
	}

	respBytes, _ := json.Marshal(getResponse.Items)

	respond(reqCtx, withJSON(fasthttp.StatusOK, respBytes))
}

func extractEtag(reqCtx *fasthttp.RequestCtx) (bool, string) {
	var etag string
	var hasEtag bool
	reqCtx.Request.Header.VisitAll(func(key []byte, value []byte) {
		if string(key) == "If-Match" {
			etag = string(value)
			hasEtag = true
			return
		}
	})

	return hasEtag, etag
}

func (a *api) onDeleteState(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getStateStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	key := reqCtx.UserValue(stateKeyParam).(string)

	concurrency := string(reqCtx.QueryArgs().Peek(concurrencyParam))
	consistency := string(reqCtx.QueryArgs().Peek(consistencyParam))

	metadata := getMetadataFromRequest(reqCtx)
	k, err := state_loader.GetModifiedStateKey(key, storeName, a.id)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(err)
		return
	}
	req := state.DeleteRequest{
		Key: k,
		Options: state.DeleteStateOption{
			Concurrency: concurrency,
			Consistency: consistency,
		},
		Metadata: metadata,
	}

	exists, etag := extractEtag(reqCtx)
	if exists {
		req.ETag = &etag
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, storeName)
	err = policy(func(ctx context.Context) error {
		return store.Delete(&req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(context.Background(), storeName, diag.Delete, err == nil, elapsed)

	if err != nil {
		statusCode, errMsg, resp := a.stateErrorResponse(err, "ERR_STATE_DELETE")
		resp.Message = fmt.Sprintf(messages.ErrStateDelete, key, errMsg)

		respond(reqCtx, withError(statusCode, resp))
		log.Debug(resp.Message)
		return
	}
	respond(reqCtx, withEmpty())
}

func (a *api) onGetSecret(reqCtx *fasthttp.RequestCtx) {
	store, secretStoreName, err := a.getSecretStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromRequest(reqCtx)

	key := reqCtx.UserValue(secretNameParam).(string)

	if !a.isSecretAllowed(secretStoreName, key) {
		msg := NewErrorResponse("ERR_PERMISSION_DENIED", fmt.Sprintf(messages.ErrPermissionDenied, key, secretStoreName))
		respond(reqCtx, withError(fasthttp.StatusForbidden, msg))
		return
	}

	req := secretstores.GetSecretRequest{
		Name:     key,
		Metadata: metadata,
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, secretStoreName)
	var resp secretstores.GetSecretResponse
	err = policy(func(ctx context.Context) (rErr error) {
		resp, rErr = store.GetSecret(req)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.SecretInvoked(context.Background(), secretStoreName, diag.Get, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_SECRET_GET",
			fmt.Sprintf(messages.ErrSecretGet, req.Name, secretStoreName, err.Error()))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if resp.Data == nil {
		respond(reqCtx, withEmpty())
		return
	}

	respBytes, _ := json.Marshal(resp.Data)
	respond(reqCtx, withJSON(fasthttp.StatusOK, respBytes))
}

func (a *api) onBulkGetSecret(reqCtx *fasthttp.RequestCtx) {
	store, secretStoreName, err := a.getSecretStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromRequest(reqCtx)

	req := secretstores.BulkGetSecretRequest{
		Metadata: metadata,
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, secretStoreName)
	var resp secretstores.BulkGetSecretResponse
	err = policy(func(ctx context.Context) (rErr error) {
		resp, rErr = store.BulkGetSecret(req)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.SecretInvoked(context.Background(), secretStoreName, diag.BulkGet, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_SECRET_GET",
			fmt.Sprintf(messages.ErrBulkSecretGet, secretStoreName, err.Error()))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if resp.Data == nil {
		respond(reqCtx, withEmpty())
		return
	}

	filteredSecrets := map[string]map[string]string{}
	for key, v := range resp.Data {
		if a.isSecretAllowed(secretStoreName, key) {
			filteredSecrets[key] = v
		} else {
			log.Debugf(messages.ErrPermissionDenied, key, secretStoreName)
		}
	}

	respBytes, _ := json.Marshal(filteredSecrets)
	respond(reqCtx, withJSON(fasthttp.StatusOK, respBytes))
}

func (a *api) getSecretStoreWithRequestValidation(reqCtx *fasthttp.RequestCtx) (secretstores.SecretStore, string, error) {
	if a.secretStores == nil || len(a.secretStores) == 0 {
		msg := NewErrorResponse("ERR_SECRET_STORES_NOT_CONFIGURED", messages.ErrSecretStoreNotConfigured)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		return nil, "", errors.New(msg.Message)
	}

	secretStoreName := reqCtx.UserValue(secretStoreNameParam).(string)

	if a.secretStores[secretStoreName] == nil {
		msg := NewErrorResponse("ERR_SECRET_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrSecretStoreNotFound, secretStoreName))
		respond(reqCtx, withError(fasthttp.StatusUnauthorized, msg))
		return nil, "", errors.New(msg.Message)
	}
	return a.secretStores[secretStoreName], secretStoreName, nil
}

func (a *api) onPostState(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getStateStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	reqs := []state.SetRequest{}
	err = json.Unmarshal(reqCtx.PostBody(), &reqs)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}
	if len(reqs) == 0 {
		respond(reqCtx, withEmpty())
		return
	}

	metadata := getMetadataFromRequest(reqCtx)

	for i, r := range reqs {
		// merge metadata from URL query parameters
		if reqs[i].Metadata == nil {
			reqs[i].Metadata = metadata
		} else {
			for k, v := range metadata {
				reqs[i].Metadata[k] = v
			}
		}

		reqs[i].Key, err = state_loader.GetModifiedStateKey(r.Key, storeName, a.id)
		if err != nil {
			msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
			respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(err)
			return
		}

		if encryption.EncryptedStateStore(storeName) {
			data := []byte(fmt.Sprintf("%v", r.Value))
			val, encErr := encryption.TryEncryptValue(storeName, data)
			if encErr != nil {
				statusCode, errMsg, resp := a.stateErrorResponse(encErr, "ERR_STATE_SAVE")
				resp.Message = fmt.Sprintf(messages.ErrStateSave, storeName, errMsg)

				respond(reqCtx, withError(statusCode, resp))
				log.Debug(resp.Message)
				return
			}

			reqs[i].Value = val
		}
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, storeName)
	err = policy(func(ctx context.Context) error {
		return store.BulkSet(reqs)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(context.Background(), storeName, diag.Set, err == nil, elapsed)

	if err != nil {
		storeName := a.getStateStoreName(reqCtx)

		statusCode, errMsg, resp := a.stateErrorResponse(err, "ERR_STATE_SAVE")
		resp.Message = fmt.Sprintf(messages.ErrStateSave, storeName, errMsg)

		respond(reqCtx, withError(statusCode, resp))
		log.Debug(resp.Message)
		return
	}

	respond(reqCtx, withEmpty())
}

// stateErrorResponse takes a state store error and returns a corresponding status code, error message and modified user error.
func (a *api) stateErrorResponse(err error, errorCode string) (int, string, ErrorResponse) {
	var message string
	var code int
	var etag bool
	etag, code, message = a.etagError(err)

	r := ErrorResponse{
		ErrorCode: errorCode,
	}
	if etag {
		return code, message, r
	}
	message = err.Error()

	return fasthttp.StatusInternalServerError, message, r
}

// etagError checks if the error from the state store is an etag error and returns a bool for indication,
// an status code and an error message.
func (a *api) etagError(err error) (bool, int, string) {
	e, ok := err.(*state.ETagError)
	if !ok {
		return false, -1, ""
	}
	switch e.Kind() {
	case state.ETagMismatch:
		return true, fasthttp.StatusConflict, e.Error()
	case state.ETagInvalid:
		return true, fasthttp.StatusBadRequest, e.Error()
	}

	return false, -1, ""
}

func (a *api) getStateStoreName(reqCtx *fasthttp.RequestCtx) string {
	return reqCtx.UserValue(storeNameParam).(string)
}

func (a *api) onDirectMessage(reqCtx *fasthttp.RequestCtx) {
	targetID := a.findTargetID(reqCtx)
	if targetID == "" {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", messages.ErrDirectInvokeNoAppID)
		respond(reqCtx, withError(fasthttp.StatusNotFound, msg))
		return
	}

	verb := strings.ToUpper(string(reqCtx.Method()))
	invokeMethodName := reqCtx.UserValue(methodParam).(string)

	if a.directMessaging == nil {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", messages.ErrDirectInvokeNotReady)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		return
	}

	// Construct internal invoke method request
	req := invokev1.NewInvokeMethodRequest(invokeMethodName).WithHTTPExtension(verb, reqCtx.QueryArgs().String())
	req.WithRawData(reqCtx.Request.Body(), string(reqCtx.Request.Header.ContentType()))
	// Save headers to internal metadata
	req.WithFastHTTPHeaders(&reqCtx.Request.Header)

	policy := a.resiliency.EndpointPolicy(reqCtx, targetID, fmt.Sprintf("%s:%s", targetID, invokeMethodName))
	// Since we don't want to return the actual error, we have to extract several things in order to construct our response.
	var resp *invokev1.InvokeMethodResponse
	var body []byte
	var statusCode int
	var msg ErrorResponse
	errorOccurred := false
	err := policy(func(ctx context.Context) (rErr error) {
		resp, rErr = a.directMessaging.Invoke(ctx, targetID, req)

		if rErr != nil {
			// Allowlists policies that are applied on the callee side can return a Permission Denied error.
			// For everything else, treat it as a gRPC transport error
			errorOccurred = true
			statusCode = fasthttp.StatusInternalServerError
			if status.Code(rErr) == codes.PermissionDenied {
				statusCode = invokev1.HTTPStatusFromCode(codes.PermissionDenied)
			}
			msg = NewErrorResponse("ERR_DIRECT_INVOKE", fmt.Sprintf(messages.ErrDirectInvoke, targetID, rErr))
			return rErr
		}

		errorOccurred = false
		invokev1.InternalMetadataToHTTPHeader(reqCtx, resp.Headers(), reqCtx.Response.Header.Set)
		var contentType string
		contentType, body = resp.RawData()
		reqCtx.Response.Header.SetContentType(contentType)

		// Construct response
		statusCode = int(resp.Status().Code)
		if !resp.IsHTTPResponse() {
			statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
			if statusCode != fasthttp.StatusOK {
				if body, rErr = invokev1.ProtobufToJSON(resp.Status()); rErr != nil {
					errorOccurred = true
					msg = NewErrorResponse("ERR_MALFORMED_RESPONSE", rErr.Error())
					statusCode = fasthttp.StatusInternalServerError
					return rErr
				}
			}
		} else if statusCode != fasthttp.StatusOK {
			return errors.Errorf("Received non-successful status code: %d", statusCode)
		}
		return nil
	})

	// Special case for timeouts/circuit breakers since they won't go through the rest of the logic.
	if errors.Is(err, context.DeadlineExceeded) || breaker.IsErrorPermanent(err) {
		respond(reqCtx, withError(500, NewErrorResponse("ERR_DIRECT_INVOKE", err.Error())))
		return
	}

	if errorOccurred {
		respond(reqCtx, withError(statusCode, msg))
		return
	}
	respond(reqCtx, with(statusCode, body))
}

// findTargetID tries to find ID of the target service from the following three places:
// 1. {id} in the URL's path.
// 2. Basic authentication, http://dapr-app-id:<service-id>@localhost:3500/path.
// 3. HTTP header: 'dapr-app-id'.
func (a *api) findTargetID(reqCtx *fasthttp.RequestCtx) string {
	if id := reqCtx.UserValue(idParam); id == nil {
		if appID := reqCtx.Request.Header.Peek(daprAppID); appID == nil {
			if auth := reqCtx.Request.Header.Peek(fasthttp.HeaderAuthorization); auth != nil &&
				strings.HasPrefix(string(auth), "Basic ") {
				if s, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(string(auth), "Basic ")); err == nil {
					pair := strings.Split(string(s), ":")
					if len(pair) == 2 && pair[0] == daprAppID {
						return pair[1]
					}
				}
			}
		} else {
			return string(appID)
		}
	} else {
		return id.(string)
	}

	return ""
}

func (a *api) onCreateActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	var req actors.CreateReminderRequest
	err := json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.CreateReminder(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_CREATE", fmt.Sprintf(messages.ErrActorReminderCreate, err))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withEmpty())
	}
}

func (a *api) onRenameActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	var req actors.RenameReminderRequest
	err := json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req.OldName = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.RenameReminder(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_RENAME", fmt.Sprintf(messages.ErrActorReminderRename, err))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withEmpty())
	}
}

func (a *api) onCreateActorTimer(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	var req actors.CreateTimerRequest
	err := json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.CreateTimer(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_TIMER_CREATE", fmt.Sprintf(messages.ErrActorTimerCreate, err))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withEmpty())
	}
}

func (a *api) onDeleteActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
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
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withEmpty())
	}
}

func (a *api) onActorStateTransaction(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	body := reqCtx.PostBody()

	var ops []actors.TransactionalOperation
	err := json.Unmarshal(body, &ops)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	hosted := a.actor.IsActorHosted(reqCtx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", messages.ErrActorInstanceMissing)
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
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
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withEmpty())
	}
}

func (a *api) onGetActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
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
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	b, err := json.Marshal(resp)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", fmt.Sprintf(messages.ErrActorReminderGet, err))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	respond(reqCtx, withJSON(fasthttp.StatusOK, b))
}

func (a *api) onDeleteActorTimer(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
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
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withEmpty())
	}
}

func (a *api) onDirectActorMessage(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
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

	// Save headers to metadata.
	metadata := map[string][]string{}
	reqCtx.Request.Header.VisitAll(func(key []byte, value []byte) {
		metadata[string(key)] = []string{string(value)}
	})
	req.WithMetadata(metadata)

	// Unlike other actor calls, resiliency is handled here for invocation.
	// This is due to actor invocation involving a lookup for the host.
	// Having the retry here allows us to capture that and be resilient to host failure.
	// Additionally, we don't perform timeouts at this level. This is because an actor
	// should technically wait forever on the locking mechanism. If we timeout while
	// waiting for the lock, we can also create a queue of calls that will try and continue
	// after the timeout.
	policy := a.resiliency.ActorPreLockPolicy(reqCtx, actorType, actorID)
	var resp *invokev1.InvokeMethodResponse
	err := policy(func(ctx context.Context) (rErr error) {
		resp, rErr = a.actor.Call(ctx, req)
		return rErr
	})
	if err != nil && !errors.Is(err, actors.ErrDaprResponseHeader) {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, err))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	invokev1.InternalMetadataToHTTPHeader(reqCtx, resp.Headers(), reqCtx.Response.Header.Set)
	contentType, body := resp.RawData()
	reqCtx.Response.Header.SetContentType(contentType)

	// Construct response.
	statusCode := int(resp.Status().Code)
	if !resp.IsHTTPResponse() {
		statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
	}
	respond(reqCtx, with(statusCode, body))
}

func (a *api) onGetActorState(reqCtx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
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
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
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
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		if resp == nil || resp.Data == nil {
			respond(reqCtx, withEmpty())
			return
		}
		respond(reqCtx, withJSON(fasthttp.StatusOK, resp.Data))
	}
}

func (a *api) onGetMetadata(reqCtx *fasthttp.RequestCtx) {
	temp := make(map[string]string)

	// Copy synchronously so it can be serialized to JSON.
	a.extendedMetadata.Range(func(key, value interface{}) bool {
		temp[key.(string)] = value.(string)

		return true
	})
	temp[daprRuntimeVersionKey] = a.daprRunTimeVersion
	activeActorsCount := []actors.ActiveActorsCount{}
	if a.actor != nil {
		activeActorsCount = a.actor.GetActiveActorsCount(reqCtx)
	}
	componentsCapabilties := a.getComponentsCapabilitesFn()
	components := a.getComponentsFn()
	registeredComponents := make([]registeredComponent, 0, len(components))
	for _, comp := range components {
		registeredComp := registeredComponent{
			Name:         comp.Name,
			Version:      comp.Spec.Version,
			Type:         comp.Spec.Type,
			Capabilities: getOrDefaultCapabilites(componentsCapabilties, comp.Name),
		}
		registeredComponents = append(registeredComponents, registeredComp)
	}

	mtd := metadata{
		ID:                   a.id,
		ActiveActorsCount:    activeActorsCount,
		Extended:             temp,
		RegisteredComponents: registeredComponents,
	}

	mtdBytes, err := json.Marshal(mtd)
	if err != nil {
		msg := NewErrorResponse("ERR_METADATA_GET", fmt.Sprintf(messages.ErrMetadataGet, err))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withJSON(fasthttp.StatusOK, mtdBytes))
	}
}

func getOrDefaultCapabilites(dict map[string][]string, key string) []string {
	if val, ok := dict[key]; ok {
		return val
	}
	return make([]string, 0)
}

func (a *api) onPutMetadata(reqCtx *fasthttp.RequestCtx) {
	key := fmt.Sprintf("%v", reqCtx.UserValue("key"))
	body := reqCtx.PostBody()
	a.extendedMetadata.Store(key, string(body))
	respond(reqCtx, withEmpty())
}

func (a *api) onShutdown(reqCtx *fasthttp.RequestCtx) {
	if !reqCtx.IsPost() {
		log.Warn("Please use POST method when invoking shutdown API")
	}

	respond(reqCtx, withEmpty())
	go func() {
		a.shutdown()
	}()
}

func (a *api) onPublish(reqCtx *fasthttp.RequestCtx) {
	if a.pubsubAdapter == nil {
		msg := NewErrorResponse("ERR_PUBSUB_NOT_CONFIGURED", messages.ErrPubsubNotConfigured)
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)

		return
	}

	pubsubName := reqCtx.UserValue(pubsubnameparam).(string)
	if pubsubName == "" {
		msg := NewErrorResponse("ERR_PUBSUB_EMPTY", messages.ErrPubsubEmpty)
		respond(reqCtx, withError(fasthttp.StatusNotFound, msg))
		log.Debug(msg)

		return
	}

	thepubsub := a.pubsubAdapter.GetPubSub(pubsubName)
	if thepubsub == nil {
		msg := NewErrorResponse("ERR_PUBSUB_NOT_FOUND", fmt.Sprintf(messages.ErrPubsubNotFound, pubsubName))
		respond(reqCtx, withError(fasthttp.StatusNotFound, msg))
		log.Debug(msg)

		return
	}

	topic := reqCtx.UserValue(topicParam).(string)
	if topic == "" {
		msg := NewErrorResponse("ERR_TOPIC_EMPTY", fmt.Sprintf(messages.ErrTopicEmpty, pubsubName))
		respond(reqCtx, withError(fasthttp.StatusNotFound, msg))
		log.Debug(msg)

		return
	}

	body := reqCtx.PostBody()
	contentType := string(reqCtx.Request.Header.Peek("Content-Type"))
	metadata := getMetadataFromRequest(reqCtx)
	rawPayload, metaErr := contrib_metadata.IsRawPayload(metadata)
	if metaErr != nil {
		msg := NewErrorResponse("ERR_PUBSUB_REQUEST_METADATA",
			fmt.Sprintf(messages.ErrMetadataGet, metaErr.Error()))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)

		return
	}

	// Extract trace context from context.
	span := diag_utils.SpanFromContext(reqCtx)
	// Populate W3C traceparent to cloudevent envelope
	corID := diag.SpanContextToW3CString(span.SpanContext())
	// Populate W3C tracestate to cloudevent envelope
	traceState := diag.TraceStateToW3CString(span.SpanContext())

	data := body

	if !rawPayload {
		envelope, err := runtime_pubsub.NewCloudEvent(&runtime_pubsub.CloudEvent{
			ID:              a.id,
			Topic:           topic,
			DataContentType: contentType,
			Data:            body,
			TraceID:         corID,
			TraceState:      traceState,
			Pubsub:          pubsubName,
		})
		if err != nil {
			msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER",
				fmt.Sprintf(messages.ErrPubsubCloudEventCreation, err.Error()))
			respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
			log.Debug(msg)
			return
		}

		features := thepubsub.Features()

		pubsub.ApplyMetadata(envelope, features, metadata)

		data, err = json.Marshal(envelope)
		if err != nil {
			msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER",
				fmt.Sprintf(messages.ErrPubsubCloudEventsSer, topic, pubsubName, err.Error()))
			respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
			log.Debug(msg)
			return
		}
	}

	req := pubsub.PublishRequest{
		PubsubName: pubsubName,
		Topic:      topic,
		Data:       data,
		Metadata:   metadata,
	}

	start := time.Now()
	err := a.pubsubAdapter.Publish(&req)
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.PubsubEgressEvent(context.Background(), pubsubName, topic, err == nil, elapsed)

	if err != nil {
		status := fasthttp.StatusInternalServerError
		msg := NewErrorResponse("ERR_PUBSUB_PUBLISH_MESSAGE",
			fmt.Sprintf(messages.ErrPubsubPublishMessage, topic, pubsubName, err.Error()))

		if errors.As(err, &runtime_pubsub.NotAllowedError{}) {
			msg = NewErrorResponse("ERR_PUBSUB_FORBIDDEN", err.Error())
			status = fasthttp.StatusForbidden
		}

		if errors.As(err, &runtime_pubsub.NotFoundError{}) {
			msg = NewErrorResponse("ERR_PUBSUB_NOT_FOUND", err.Error())
			status = fasthttp.StatusBadRequest
		}

		respond(reqCtx, withError(status, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withEmpty())
	}
}

// GetStatusCodeFromMetadata extracts the http status code from the metadata if it exists.
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
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withEmpty())
	}
}

func (a *api) onGetOutboundHealthz(reqCtx *fasthttp.RequestCtx) {
	if !a.outboundReadyStatus {
		msg := NewErrorResponse("ERR_HEALTH_NOT_READY", messages.ErrHealthNotReady)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withEmpty())
	}
}

func getMetadataFromRequest(reqCtx *fasthttp.RequestCtx) map[string]string {
	metadata := map[string]string{}
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
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_CONFIGURED", messages.ErrStateStoresNotConfigured)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	storeName := reqCtx.UserValue(storeNameParam).(string)
	_, ok := a.stateStores[storeName]
	if !ok {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrStateStoreNotFound, storeName))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	transactionalStore, ok := a.transactionalStateStores[storeName]
	if !ok {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_SUPPORTED", fmt.Sprintf(messages.ErrStateStoreNotSupported, storeName))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	body := reqCtx.PostBody()
	var req state.TransactionalStateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err.Error()))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}
	if len(req.Operations) == 0 {
		respond(reqCtx, withEmpty())
		return
	}

	// merge metadata from URL query parameters
	metadata := getMetadataFromRequest(reqCtx)
	if req.Metadata == nil {
		req.Metadata = metadata
	} else {
		for k, v := range metadata {
			req.Metadata[k] = v
		}
	}

	operations := []state.TransactionalStateOperation{}
	for _, o := range req.Operations {
		switch o.Operation {
		case state.Upsert:
			var upsertReq state.SetRequest
			err := mapstructure.Decode(o.Request, &upsertReq)
			if err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST",
					fmt.Sprintf(messages.ErrMalformedRequest, err.Error()))
				respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
				log.Debug(msg)
				return
			}
			upsertReq.Key, err = state_loader.GetModifiedStateKey(upsertReq.Key, storeName, a.id)
			if err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
				respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
				log.Debug(err)
				return
			}
			operations = append(operations, state.TransactionalStateOperation{
				Request:   upsertReq,
				Operation: state.Upsert,
			})
		case state.Delete:
			var delReq state.DeleteRequest
			err := mapstructure.Decode(o.Request, &delReq)
			if err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST",
					fmt.Sprintf(messages.ErrMalformedRequest, err.Error()))
				respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
				log.Debug(msg)
				return
			}
			delReq.Key, err = state_loader.GetModifiedStateKey(delReq.Key, storeName, a.id)
			if err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
				respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
				log.Debug(msg)
				return
			}
			operations = append(operations, state.TransactionalStateOperation{
				Request:   delReq,
				Operation: state.Delete,
			})
		default:
			msg := NewErrorResponse(
				"ERR_NOT_SUPPORTED_STATE_OPERATION",
				fmt.Sprintf(messages.ErrNotSupportedStateOperation, o.Operation))
			respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(msg)
			return
		}
	}

	if encryption.EncryptedStateStore(storeName) {
		for i, op := range operations {
			if op.Operation == state.Upsert {
				req := op.Request.(*state.SetRequest)
				data := []byte(fmt.Sprintf("%v", req.Value))
				val, err := encryption.TryEncryptValue(storeName, data)
				if err != nil {
					msg := NewErrorResponse(
						"ERR_SAVE_STATE",
						fmt.Sprintf(messages.ErrStateSave, storeName, err.Error()))
					respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
					log.Debug(msg)
					return
				}

				req.Value = val
				operations[i].Request = req
			}
		}
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, storeName)
	err := policy(func(ctx context.Context) error {
		return transactionalStore.Multi(&state.TransactionalStateRequest{
			Operations: operations,
			Metadata:   req.Metadata,
		})
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(context.Background(), storeName, diag.StateTransaction, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_STATE_TRANSACTION", fmt.Sprintf(messages.ErrStateTransaction, err.Error()))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withEmpty())
	}
}

func (a *api) onQueryState(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getStateStoreWithRequestValidation(reqCtx)
	if err != nil {
		// error has been already logged
		return
	}

	querier, ok := store.(state.Querier)
	if !ok {
		msg := NewErrorResponse("ERR_METHOD_NOT_FOUND", fmt.Sprintf(messages.ErrNotFound, "Query"))
		respond(reqCtx, withError(fasthttp.StatusNotFound, msg))
		log.Debug(msg)
		return
	}

	if encryption.EncryptedStateStore(storeName) {
		msg := NewErrorResponse("ERR_STATE_QUERY", fmt.Sprintf(messages.ErrStateQuery, storeName, "cannot query encrypted store"))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	var req state.QueryRequest
	if err = json.Unmarshal(reqCtx.PostBody(), &req.Query); err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err.Error()))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}
	req.Metadata = getMetadataFromRequest(reqCtx)

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(reqCtx, storeName)
	var resp *state.QueryResponse
	err = policy(func(ctx context.Context) (rErr error) {
		resp, rErr = querier.Query(&req)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(context.Background(), storeName, diag.StateQuery, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_STATE_QUERY", fmt.Sprintf(messages.ErrStateQuery, storeName, err.Error()))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	if resp == nil || len(resp.Results) == 0 {
		respond(reqCtx, withEmpty())
		return
	}

	qresp := QueryResponse{
		Results:  make([]QueryItem, len(resp.Results)),
		Token:    resp.Token,
		Metadata: resp.Metadata,
	}
	for i := range resp.Results {
		qresp.Results[i].Key = state_loader.GetOriginalStateKey(resp.Results[i].Key)
		qresp.Results[i].ETag = resp.Results[i].ETag
		qresp.Results[i].Error = resp.Results[i].Error
		qresp.Results[i].Data = json.RawMessage(resp.Results[i].Data)
	}

	b, _ := json.Marshal(qresp)
	respond(reqCtx, withJSON(fasthttp.StatusOK, b))
}

func (a *api) isSecretAllowed(storeName, key string) bool {
	if config, ok := a.secretsConfiguration[storeName]; ok {
		return config.IsSecretAllowed(key)
	}
	// By default, if a configuration is not defined for a secret store, return true.
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
