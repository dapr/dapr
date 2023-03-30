/*
Copyright 2022 The Dapr Authors
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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/mitchellh/mapstructure"
	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	contribCrypto "github.com/dapr/components-contrib/crypto"
	"github.com/dapr/components-contrib/lock"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	wfs "github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/actors"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/channel/http"
	lockLoader "github.com/dapr/dapr/pkg/components/lock"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/grpc/universalapi"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/utils"
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
	universal                  *universalapi.UniversalAPI
	endpoints                  []Endpoint
	publicEndpoints            []Endpoint
	directMessaging            messaging.DirectMessaging
	appChannel                 channel.AppChannel
	getComponentsFn            func() []componentsV1alpha1.Component
	getSubscriptionsFn         func() []runtimePubsub.Subscription
	resiliency                 resiliency.Provider
	stateStores                map[string]state.Store
	lockStores                 map[string]lock.Store
	configurationStores        map[string]configuration.Store
	configurationSubscribe     map[string]chan struct{}
	transactionalStateStores   map[string]state.TransactionalStore
	secretStores               map[string]secretstores.SecretStore
	secretsConfiguration       map[string]config.SecretsScope
	actor                      actors.Actors
	pubsubAdapter              runtimePubsub.Adapter
	sendToOutputBindingFn      func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	id                         string
	extendedMetadata           sync.Map
	readyStatus                bool
	outboundReadyStatus        bool
	tracingSpec                config.TracingSpec
	shutdown                   func()
	getComponentsCapabilitesFn func() map[string][]string
	daprRunTimeVersion         string
	maxRequestBodySize         int64 // In bytes
	isStreamingEnabled         bool
}

type registeredComponent struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

type pubsubSubscription struct {
	PubsubName      string                    `json:"pubsubname"`
	Topic           string                    `json:"topic"`
	DeadLetterTopic string                    `json:"deadLetterTopic"`
	Metadata        map[string]string         `json:"metadata"`
	Rules           []*pubsubSubscriptionRule `json:"rules,omitempty"`
}

type pubsubSubscriptionRule struct {
	Match string `json:"match"`
	Path  string `json:"path"`
}

type metadata struct {
	ID                   string                     `json:"id"`
	ActiveActorsCount    []actors.ActiveActorsCount `json:"actors"`
	Extended             map[string]string          `json:"extended"`
	RegisteredComponents []registeredComponent      `json:"components"`
	Subscriptions        []pubsubSubscription       `json:"subscriptions"`
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
	workflowComponent        = "workflowComponent"
	workflowName             = "workflowName"
	instanceID               = "instanceID"
	eventName                = "eventName"
	consistencyParam         = "consistency"
	concurrencyParam         = "concurrency"
	pubsubnameparam          = "pubsubname"
	traceparentHeader        = "traceparent"
	tracestateHeader         = "tracestate"
	daprAppID                = "dapr-app-id"
	daprRuntimeVersionKey    = "daprRuntimeVersion"
)

// APIOpts contains the options for NewAPI.
type APIOpts struct {
	AppID                       string
	AppChannel                  channel.AppChannel
	DirectMessaging             messaging.DirectMessaging
	GetComponentsFn             func() []componentsV1alpha1.Component
	GetSubscriptionsFn          func() []runtimePubsub.Subscription
	Resiliency                  resiliency.Provider
	StateStores                 map[string]state.Store
	WorkflowsComponents         map[string]wfs.Workflow
	LockStores                  map[string]lock.Store
	SecretStores                map[string]secretstores.SecretStore
	SecretsConfiguration        map[string]config.SecretsScope
	ConfigurationStores         map[string]configuration.Store
	CryptoProviders             map[string]contribCrypto.SubtleCrypto
	PubsubAdapter               runtimePubsub.Adapter
	Actor                       actors.Actors
	SendToOutputBindingFn       func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	TracingSpec                 config.TracingSpec
	Shutdown                    func()
	GetComponentsCapabilitiesFn func() map[string][]string
	MaxRequestBodySize          int64 // In bytes
	IsStreamingEnabled          bool
}

// NewAPI returns a new API.
func NewAPI(opts APIOpts) API {
	transactionalStateStores := map[string]state.TransactionalStore{}
	for key, store := range opts.StateStores {
		if state.FeatureTransactional.IsPresent(store.Features()) {
			transactionalStateStores[key] = store.(state.TransactionalStore)
		}
	}
	api := &api{
		id:                         opts.AppID,
		appChannel:                 opts.AppChannel,
		directMessaging:            opts.DirectMessaging,
		getComponentsFn:            opts.GetComponentsFn,
		getSubscriptionsFn:         opts.GetSubscriptionsFn,
		resiliency:                 opts.Resiliency,
		stateStores:                opts.StateStores,
		lockStores:                 opts.LockStores,
		secretStores:               opts.SecretStores,
		secretsConfiguration:       opts.SecretsConfiguration,
		configurationStores:        opts.ConfigurationStores,
		pubsubAdapter:              opts.PubsubAdapter,
		actor:                      opts.Actor,
		sendToOutputBindingFn:      opts.SendToOutputBindingFn,
		tracingSpec:                opts.TracingSpec,
		shutdown:                   opts.Shutdown,
		getComponentsCapabilitesFn: opts.GetComponentsCapabilitiesFn,
		maxRequestBodySize:         opts.MaxRequestBodySize,
		transactionalStateStores:   transactionalStateStores,
		configurationSubscribe:     make(map[string]chan struct{}),
		isStreamingEnabled:         opts.IsStreamingEnabled,
		daprRunTimeVersion:         buildinfo.Version(),
		universal: &universalapi.UniversalAPI{
			Logger:               log,
			Resiliency:           opts.Resiliency,
			CryptoProviders:      opts.CryptoProviders,
			SecretStores:         opts.SecretStores,
			SecretsConfiguration: opts.SecretsConfiguration,
			WorkflowComponents:   opts.WorkflowsComponents,
		},
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
	api.endpoints = append(api.endpoints, api.constructWorkflowEndpoints()...)

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

// Workflow Component: Component specified in yaml
// Workflow Name: Name of the workflow to run
// Instance ID: Identifier of the specific run
func (a *api) constructWorkflowEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "workflows/{workflowComponent}/{instanceID}",
			Version: apiVersionV1alpha1,
			Handler: a.onGetWorkflowHandler(),
		},
		{
			Methods: []string{fasthttp.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/raiseEvent/{eventName}",
			Version: apiVersionV1alpha1,
			Handler: a.onRaiseEventWorkflowHandler(),
		},
		{
			Methods: []string{fasthttp.MethodPost},
			Route:   "workflows/{workflowComponent}/{workflowName}/{instanceID}/start",
			Version: apiVersionV1alpha1,
			Handler: a.onStartWorkflowHandler(),
		},
		{
			Methods: []string{fasthttp.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/terminate",
			Version: apiVersionV1alpha1,
			Handler: a.onTerminateWorkflowHandler(),
		},
	}
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
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "publish/bulk/{pubsubname}/{topic:*}",
			Version: apiVersionV1alpha1,
			Handler: a.onBulkPublish,
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
			Methods:       []string{fasthttp.MethodGet},
			Route:         "healthz",
			Version:       apiVersionV1,
			Handler:       a.onGetHealthz,
			AlwaysAllowed: true,
			IsHealthCheck: true,
		},
		{
			Methods:       []string{fasthttp.MethodGet},
			Route:         "healthz/outbound",
			Version:       apiVersionV1,
			Handler:       a.onGetOutboundHealthz,
			AlwaysAllowed: true,
			IsHealthCheck: true,
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
	if span := diagUtils.SpanFromContext(reqCtx); span != nil {
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

type bulkGetRes struct {
	bulkGet   bool
	responses []state.BulkGetResponse
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
	var key string
	reqs := make([]state.GetRequest, len(req.Keys))
	for i, k := range req.Keys {
		key, err = stateLoader.GetModifiedStateKey(k, storeName, a.id)
		if err != nil {
			msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
			respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(err)
			return
		}
		r := state.GetRequest{
			Key:      key,
			Metadata: req.Metadata,
		}
		reqs[i] = r
	}

	start := time.Now()
	policyDef := a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore)
	bgrPolicyRunner := resiliency.NewRunner[*bulkGetRes](reqCtx, policyDef)
	bgr, rErr := bgrPolicyRunner(func(ctx context.Context) (*bulkGetRes, error) {
		rBulkGet, rBulkResponse, rErr := store.BulkGet(ctx, reqs)
		return &bulkGetRes{
			bulkGet:   rBulkGet,
			responses: rBulkResponse,
		}, rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(context.Background(), storeName, diag.BulkGet, err == nil, elapsed)

	if bgr == nil {
		bgr = &bulkGetRes{}
	}

	if bgr.bulkGet {
		// if store supports bulk get
		if rErr != nil {
			msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
			respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(msg)
			return
		}

		for i := 0; i < len(bgr.responses) && i < len(req.Keys); i++ {
			bulkResp[i].Key = stateLoader.GetOriginalStateKey(bgr.responses[i].Key)
			if bgr.responses[i].Error != "" {
				log.Debugf("bulk get: error getting key %s: %s", bulkResp[i].Key, bgr.responses[i].Error)
				bulkResp[i].Error = bgr.responses[i].Error
			} else {
				bulkResp[i].Data = json.RawMessage(bgr.responses[i].Data)
				bulkResp[i].ETag = bgr.responses[i].ETag
				bulkResp[i].Metadata = bgr.responses[i].Metadata
			}
		}
	} else {
		// if store doesn't support bulk get, fallback to call get() method one by one
		limiter := concurrency.NewLimiter(req.Parallelism)

		for i, k := range req.Keys {
			bulkResp[i].Key = k

			fn := func(param interface{}) {
				r := param.(*BulkGetResponse)
				k, err := stateLoader.GetModifiedStateKey(r.Key, storeName, a.id)
				if err != nil {
					log.Debug(err)
					r.Error = err.Error()
					return
				}

				policyRunner := resiliency.NewRunner[*state.GetResponse](reqCtx, policyDef)
				resp, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
					return store.Get(ctx, &state.GetRequest{
						Key:      k,
						Metadata: metadata,
					})
				})
				if err != nil {
					log.Debugf("Bulk get: error getting key %s: %v", r.Key, err)
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
			if bulkResp[i].Error != "" || len(bulkResp[i].Data) == 0 {
				bulkResp[i].Data = nil
				continue
			}

			val, err := encryption.TryDecryptValue(storeName, bulkResp[i].Data)
			if err != nil {
				log.Debugf("Bulk get error: %v", err)
				bulkResp[i].Data = nil
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

// Route:   "workflows/{workflowComponent}/{workflowName}/{instanceId}",
// Workflow Component: Component specified in yaml
// Workflow Name: Name of the workflow to run
// Instance ID: Identifier of the specific run
func (a *api) onStartWorkflowHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.StartWorkflowAlpha1,
		UniversalFastHTTPHandlerOpts[*runtimev1pb.StartWorkflowRequest, *runtimev1pb.WorkflowReference]{
			InModifier: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.StartWorkflowRequest, error) {
				in.WorkflowName = reqCtx.UserValue(workflowName).(string)
				in.WorkflowComponent = reqCtx.UserValue(workflowComponent).(string)
				in.InstanceId = reqCtx.UserValue(instanceID).(string)
				return in, nil
			},
			SuccessStatusCode: fasthttp.StatusAccepted,
		})
}

func (a *api) onGetWorkflowHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.GetWorkflowAlpha1,
		UniversalFastHTTPHandlerOpts[*runtimev1pb.GetWorkflowRequest, *runtimev1pb.GetWorkflowResponse]{
			InModifier: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowRequest, error) {
				in.WorkflowComponent = reqCtx.UserValue(workflowComponent).(string)
				in.InstanceId = reqCtx.UserValue(instanceID).(string)
				return in, nil
			},
		})
}

func (a *api) onTerminateWorkflowHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.TerminateWorkflowAlpha1,
		UniversalFastHTTPHandlerOpts[*runtimev1pb.TerminateWorkflowRequest, *runtimev1pb.TerminateWorkflowResponse]{
			InModifier: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.TerminateWorkflowRequest) (*runtimev1pb.TerminateWorkflowRequest, error) {
				in.WorkflowComponent = reqCtx.UserValue(workflowComponent).(string)
				in.InstanceId = reqCtx.UserValue(instanceID).(string)
				return in, nil
			},
			SuccessStatusCode: fasthttp.StatusAccepted,
		})
}

func (a *api) onRaiseEventWorkflowHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.RaiseEventWorkflowAlpha1,
		UniversalFastHTTPHandlerOpts[*runtimev1pb.RaiseEventWorkflowRequest, *runtimev1pb.RaiseEventWorkflowResponse]{
			InModifier: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.RaiseEventWorkflowRequest) (*runtimev1pb.RaiseEventWorkflowRequest, error) {
				in.InstanceId = reqCtx.UserValue(instanceID).(string)
				in.WorkflowComponent = reqCtx.UserValue(workflowComponent).(string)
				in.EventName = reqCtx.UserValue(eventName).(string)
				return in, nil
			},
			SuccessStatusCode: fasthttp.StatusAccepted,
		})
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
	k, err := stateLoader.GetModifiedStateKey(key, storeName, a.id)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(err)
		return
	}
	req := &state.GetRequest{
		Key: k,
		Options: state.GetStateOption{
			Consistency: consistency,
		},
		Metadata: metadata,
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*state.GetResponse](reqCtx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	resp, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
		return store.Get(ctx, req)
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

type UnsubscribeConfigurationResponse struct {
	Ok      bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	Message string `protobuf:"bytes,2,opt,name=message,proto3" json:"message,omitempty"`
}

type configurationEventHandler struct {
	api        *api
	storeName  string
	appChannel channel.AppChannel
	res        resiliency.Provider
}

func (h *configurationEventHandler) updateEventHandler(ctx context.Context, e *configuration.UpdateEvent) error {
	if h.appChannel == nil {
		err := fmt.Errorf("app channel is nil. unable to send configuration update from %s", h.storeName)
		log.Error(err)
		return err
	}
	for key := range e.Items {
		policyDef := h.res.ComponentInboundPolicy(h.storeName, resiliency.Configuration)

		eventBody := &bytes.Buffer{}
		_ = json.NewEncoder(eventBody).Encode(e)

		req := invokev1.NewInvokeMethodRequest("/configuration/"+h.storeName+"/"+key).
			WithHTTPExtension(nethttp.MethodPost, "").
			WithRawData(eventBody).
			WithContentType(invokev1.JSONContentType)
		if policyDef != nil {
			req.WithReplay(policyDef.HasRetries())
		}
		defer req.Close()

		policyRunner := resiliency.NewRunner[any](ctx, policyDef)
		_, err := policyRunner(func(ctx context.Context) (any, error) {
			rResp, rErr := h.appChannel.InvokeMethod(ctx, req)
			if rErr != nil {
				return nil, rErr
			}
			if rResp != nil {
				defer rResp.Close()
			}

			if rResp != nil && rResp.Status().Code != nethttp.StatusOK {
				return nil, fmt.Errorf("error sending configuration item to application, status %d", rResp.Status().Code)
			}
			return nil, nil
		})
		if err != nil {
			log.Errorf("error sending configuration item to the app: %v", err)
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

	req.ResourceID, err = lockLoader.GetModifiedLockKey(req.ResourceID, storeName, a.id)
	if err != nil {
		msg := NewErrorResponse("ERR_TRY_LOCK", err.Error())
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	policyRunner := resiliency.NewRunner[*lock.TryLockResponse](reqCtx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Lock),
	)
	resp, err := policyRunner(func(ctx context.Context) (*lock.TryLockResponse, error) {
		return store.TryLock(ctx, &req)
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

	req := &lock.UnlockRequest{}
	err = json.Unmarshal(reqCtx.PostBody(), req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req.ResourceID, err = lockLoader.GetModifiedLockKey(req.ResourceID, storeName, a.id)
	if err != nil {
		msg := NewErrorResponse("ERR_UNLOCK", err.Error())
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	policyRunner := resiliency.NewRunner[*lock.UnlockResponse](reqCtx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Lock),
	)
	resp, err := policyRunner(func(ctx context.Context) (*lock.UnlockResponse, error) {
		return store.Unlock(ctx, req)
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
	if a.appChannel == nil {
		msg := NewErrorResponse("ERR_APP_CHANNEL_NIL", "app channel is not initialized. cannot subscribe to configuration updates")
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	metadata := getMetadataFromRequest(reqCtx)
	subscribeKeys := make([]string, 0)

	keys := make([]string, 0)
	queryKeys := reqCtx.QueryArgs().PeekMulti(configurationKeyParam)
	for _, queryKeyByte := range queryKeys {
		keys = append(keys, string(queryKeyByte))
	}

	if len(keys) > 0 {
		subscribeKeys = append(subscribeKeys, keys...)
	}

	req := &configuration.SubscribeRequest{
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
	policyRunner := resiliency.NewRunner[string](reqCtx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Configuration),
	)
	subscribeID, err := policyRunner(func(ctx context.Context) (string, error) {
		return store.Subscribe(ctx, req, handler.updateEventHandler)
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
	policyRunner := resiliency.NewRunner[any](reqCtx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Configuration),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.Unsubscribe(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)
	diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), storeName, diag.ConfigurationUnsubscribe, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_CONFIGURATION_UNSUBSCRIBE", fmt.Sprintf(messages.ErrConfigurationUnsubscribe, subscribeID, err.Error()))
		errRespBytes, _ := json.Marshal(&UnsubscribeConfigurationResponse{
			Ok:      false,
			Message: msg.Message,
		})
		respond(reqCtx, withJSON(fasthttp.StatusInternalServerError, errRespBytes))
		log.Debug(msg)
		return
	}
	respBytes, _ := json.Marshal(&UnsubscribeConfigurationResponse{
		Ok: true,
	})
	respond(reqCtx, withJSON(fasthttp.StatusOK, respBytes))
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
	req := &configuration.GetRequest{
		Keys:     keys,
		Metadata: metadata,
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*configuration.GetResponse](reqCtx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Configuration),
	)
	getResponse, err := policyRunner(func(ctx context.Context) (*configuration.GetResponse, error) {
		return store.Get(ctx, req)
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

func extractEtag(reqCtx *fasthttp.RequestCtx) (hasEtag bool, etag string) {
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
	k, err := stateLoader.GetModifiedStateKey(key, storeName, a.id)
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
	policyRunner := resiliency.NewRunner[any](reqCtx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.Delete(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(reqCtx, storeName, diag.Delete, err == nil, elapsed)

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
		apiErr := messages.ErrSecretPermissionDenied.WithFormat(key, secretStoreName)
		msg := NewErrorResponse(apiErr.Tag(), apiErr.Message())
		respond(reqCtx, withError(apiErr.HTTPCode(), msg))
		return
	}

	req := secretstores.GetSecretRequest{
		Name:     key,
		Metadata: metadata,
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*secretstores.GetSecretResponse](reqCtx,
		a.resiliency.ComponentOutboundPolicy(secretStoreName, resiliency.Secretstore),
	)
	resp, err := policyRunner(func(ctx context.Context) (*secretstores.GetSecretResponse, error) {
		rResp, rErr := store.GetSecret(ctx, req)
		return &rResp, rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.SecretInvoked(context.Background(), secretStoreName, diag.Get, err == nil, elapsed)

	if err != nil {
		apiErr := messages.ErrSecretGet.WithFormat(req.Name, secretStoreName, err.Error())
		msg := NewErrorResponse(apiErr.Tag(), apiErr.Message())
		respond(reqCtx, withError(apiErr.HTTPCode(), msg))
		log.Debug(msg)
		return
	}

	if resp == nil || resp.Data == nil {
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
	policyRunner := resiliency.NewRunner[*secretstores.BulkGetSecretResponse](reqCtx,
		a.resiliency.ComponentOutboundPolicy(secretStoreName, resiliency.Secretstore),
	)
	resp, err := policyRunner(func(ctx context.Context) (*secretstores.BulkGetSecretResponse, error) {
		rResp, rErr := store.BulkGetSecret(ctx, req)
		return &rResp, rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.SecretInvoked(context.Background(), secretStoreName, diag.BulkGet, err == nil, elapsed)

	if err != nil {
		apiErr := messages.ErrBulkSecretGet.WithFormat(secretStoreName, err.Error())
		msg := NewErrorResponse(apiErr.Tag(), apiErr.Message())
		respond(reqCtx, withError(apiErr.HTTPCode(), msg))
		log.Debug(msg)
		return
	}

	if resp == nil || resp.Data == nil {
		respond(reqCtx, withEmpty())
		return
	}

	filteredSecrets := map[string]map[string]string{}
	for key, v := range resp.Data {
		if a.isSecretAllowed(secretStoreName, key) {
			filteredSecrets[key] = v
		} else {
			log.Debugf(messages.ErrSecretPermissionDenied.WithFormat(key, secretStoreName).String())
		}
	}

	respBytes, _ := json.Marshal(filteredSecrets)
	respond(reqCtx, withJSON(fasthttp.StatusOK, respBytes))
}

func (a *api) getSecretStoreWithRequestValidation(reqCtx *fasthttp.RequestCtx) (secretstores.SecretStore, string, error) {
	if a.secretStores == nil || len(a.secretStores) == 0 {
		apiErr := messages.ErrSecretStoreNotConfigured
		msg := NewErrorResponse(apiErr.Tag(), apiErr.Message())
		respond(reqCtx, withError(apiErr.HTTPCode(), msg))
		return nil, "", errors.New(msg.Message)
	}

	secretStoreName := reqCtx.UserValue(secretStoreNameParam).(string)

	if a.secretStores[secretStoreName] == nil {
		apiErr := messages.ErrSecretStoreNotFound.WithFormat(secretStoreName)
		msg := NewErrorResponse(apiErr.Tag(), apiErr.Message())
		respond(reqCtx, withError(apiErr.HTTPCode(), msg))
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

		reqs[i].Key, err = stateLoader.GetModifiedStateKey(r.Key, storeName, a.id)
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
	policyRunner := resiliency.NewRunner[any](reqCtx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.BulkSet(ctx, reqs)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(reqCtx, storeName, diag.Set, err == nil, elapsed)

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

type invokeError struct {
	statusCode int
	msg        ErrorResponse
}

func (ie invokeError) Error() string {
	return fmt.Sprintf("invokeError (statusCode='%d') msg.errorCode='%s' msg.message='%s'", ie.statusCode, ie.msg.ErrorCode, ie.msg.Message)
}

func (a *api) onDirectMessage(reqCtx *fasthttp.RequestCtx) {
	// Need a context specific to this request. See: https://github.com/valyala/fasthttp/issues/1350
	// Because this can respond with `withStream()`, we can't defer a call to cancel() here
	ctx, cancel := context.WithCancel(reqCtx)

	targetID := a.findTargetID(reqCtx)
	if targetID == "" {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", messages.ErrDirectInvokeNoAppID)
		respond(reqCtx, withError(fasthttp.StatusNotFound, msg))
		cancel()
		return
	}

	verb := strings.ToUpper(string(reqCtx.Method()))
	invokeMethodName := reqCtx.UserValue(methodParam).(string)

	if a.directMessaging == nil {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", messages.ErrDirectInvokeNotReady)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		cancel()
		return
	}

	policyDef := a.resiliency.EndpointPolicy(targetID, targetID+":"+invokeMethodName)

	// Construct internal invoke method request
	req := invokev1.NewInvokeMethodRequest(invokeMethodName).
		WithHTTPExtension(verb, reqCtx.QueryArgs().String()).
		WithRawDataBytes(reqCtx.Request.Body()).
		WithContentType(string(reqCtx.Request.Header.ContentType())).
		// Save headers to internal metadata
		WithFastHTTPHeaders(&reqCtx.Request.Header)
	if policyDef != nil {
		req.WithReplay(policyDef.HasRetries())
	}
	defer req.Close()

	policyRunner := resiliency.NewRunnerWithOptions(
		ctx, policyDef,
		resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
			Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
		},
	)
	// Since we don't want to return the actual error, we have to extract several things in order to construct our response.
	resp, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		rResp, rErr := a.directMessaging.Invoke(ctx, targetID, req)
		if rErr != nil {
			// Allowlist policies that are applied on the callee side can return a Permission Denied error.
			// For everything else, treat it as a gRPC transport error
			invokeErr := invokeError{
				statusCode: fasthttp.StatusInternalServerError,
				msg:        NewErrorResponse("ERR_DIRECT_INVOKE", fmt.Sprintf(messages.ErrDirectInvoke, targetID, rErr)),
			}
			if status.Code(rErr) == codes.PermissionDenied {
				invokeErr.statusCode = invokev1.HTTPStatusFromCode(codes.PermissionDenied)
			}
			return rResp, invokeErr
		}

		// Construct response if not HTTP
		resStatus := rResp.Status()
		if !rResp.IsHTTPResponse() {
			statusCode := int32(invokev1.HTTPStatusFromCode(codes.Code(resStatus.Code)))
			if statusCode != fasthttp.StatusOK {
				// Close the response to replace the body
				_ = rResp.Close()
				var body []byte
				body, rErr = invokev1.ProtobufToJSON(resStatus)
				rResp.WithRawDataBytes(body)
				resStatus.Code = statusCode
				if rErr != nil {
					return rResp, invokeError{
						statusCode: fasthttp.StatusInternalServerError,
						msg:        NewErrorResponse("ERR_MALFORMED_RESPONSE", rErr.Error()),
					}
				}
			} else {
				resStatus.Code = statusCode
			}
		} else if resStatus.Code < 200 || resStatus.Code > 399 {
			// We are not returning an `invokeError` here on purpose.
			// Returning an error that is not an `invokeError` will cause Resiliency to retry the request (if retries are enabled), but if the request continues to fail, the response is sent to the user with whatever status code the app returned so the "received non-successful status code" is "swallowed" (will appear in logs but won't be returned to the app).
			return rResp, fmt.Errorf("received non-successful status code: %d", resStatus.Code)
		}
		return rResp, nil
	})

	// Special case for timeouts/circuit breakers since they won't go through the rest of the logic.
	if errors.Is(err, context.DeadlineExceeded) || breaker.IsErrorPermanent(err) {
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, NewErrorResponse("ERR_DIRECT_INVOKE", err.Error())))
		cancel()
		return
	}

	if resp != nil {
		headers := resp.Headers()
		if len(headers) > 0 {
			invokev1.InternalMetadataToHTTPHeader(reqCtx, headers, reqCtx.Response.Header.Set)
		}
	}

	invokeErr := invokeError{}
	if errors.As(err, &invokeErr) {
		respond(reqCtx, withError(invokeErr.statusCode, invokeErr.msg))
		cancel()
		if resp != nil {
			_ = resp.Close()
		}
		return
	}

	if resp == nil {
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, NewErrorResponse("ERR_DIRECT_INVOKE", fmt.Sprintf(messages.ErrDirectInvoke, targetID, "response object is nil"))))
		cancel()
		return
	}

	statusCode := int(resp.Status().Code)

	// TODO @ItalyPaleAle: Make this the only path once streaming is finalized
	if a.isStreamingEnabled {
		// This will also close the response stream automatically; no need to invoke resp.Close()
		// Likewise, it calls "cancel" to cancel the context at the end
		respond(reqCtx, withStream(statusCode, resp.RawData(), cancel))
		return
	}

	defer resp.Close()

	body, err := resp.RawDataFull()
	if err != nil {
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, NewErrorResponse("ERR_DIRECT_INVOKE", fmt.Sprintf(messages.ErrDirectInvoke, targetID, err))))
		cancel()
		return
	}

	reqCtx.Response.Header.SetContentType(resp.ContentType())
	respond(reqCtx, with(statusCode, body))
	cancel()
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

	metadata := make(map[string][]string, reqCtx.Request.Header.Len())
	reqCtx.Request.Header.VisitAll(func(key []byte, value []byte) {
		metadata[string(key)] = []string{string(value)}
	})

	policyDef := a.resiliency.ActorPreLockPolicy(actorType, actorID)

	req := invokev1.NewInvokeMethodRequest(method).
		WithActor(actorType, actorID).
		WithHTTPExtension(verb, reqCtx.QueryArgs().String()).
		WithRawDataBytes(reqCtx.PostBody()).
		WithContentType(string(reqCtx.Request.Header.ContentType())).
		// Save headers to metadata
		WithMetadata(metadata)
	if policyDef != nil {
		req.WithReplay(policyDef.HasRetries())
	}
	defer req.Close()

	// Unlike other actor calls, resiliency is handled here for invocation.
	// This is due to actor invocation involving a lookup for the host.
	// Having the retry here allows us to capture that and be resilient to host failure.
	// Additionally, we don't perform timeouts at this level. This is because an actor
	// should technically wait forever on the locking mechanism. If we timeout while
	// waiting for the lock, we can also create a queue of calls that will try and continue
	// after the timeout.
	policyRunner := resiliency.NewRunnerWithOptions(reqCtx, policyDef,
		resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
			Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
		},
	)
	resp, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return a.actor.Call(ctx, req)
	})
	if err != nil && !errors.Is(err, actors.ErrDaprResponseHeader) {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, err))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if resp == nil {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, "failed to cast response"))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	defer resp.Close()

	invokev1.InternalMetadataToHTTPHeader(reqCtx, resp.Headers(), reqCtx.Response.Header.Set)
	body, err := resp.RawDataFull()
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, err))
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	reqCtx.Response.Header.SetContentType(resp.ContentType())

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

	subscriptions := a.getSubscriptionsFn()
	ps := []pubsubSubscription{}
	for _, s := range subscriptions {
		ps = append(ps, pubsubSubscription{
			PubsubName:      s.PubsubName,
			Topic:           s.Topic,
			Metadata:        s.Metadata,
			DeadLetterTopic: s.DeadLetterTopic,
			Rules:           convertPubsubSubscriptionRules(s.Rules),
		})
	}

	mtd := metadata{
		ID:                   a.id,
		ActiveActorsCount:    activeActorsCount,
		Extended:             temp,
		RegisteredComponents: registeredComponents,
		Subscriptions:        ps,
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

func convertPubsubSubscriptionRules(rules []*runtimePubsub.Rule) []*pubsubSubscriptionRule {
	out := make([]*pubsubSubscriptionRule, 0)
	for _, r := range rules {
		out = append(out, &pubsubSubscriptionRule{
			Match: fmt.Sprintf("%s", r.Match),
			Path:  r.Path,
		})
	}
	return out
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
	thepubsub, pubsubName, topic, sc, errRes := a.validateAndGetPubsubAndTopic(reqCtx)
	if errRes != nil {
		respond(reqCtx, withError(sc, *errRes))

		return
	}

	body := reqCtx.PostBody()
	contentType := string(reqCtx.Request.Header.Peek("Content-Type"))
	metadata := getMetadataFromRequest(reqCtx)
	rawPayload, metaErr := contribMetadata.IsRawPayload(metadata)
	if metaErr != nil {
		msg := NewErrorResponse("ERR_PUBSUB_REQUEST_METADATA",
			fmt.Sprintf(messages.ErrMetadataGet, metaErr.Error()))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)

		return
	}

	// Extract trace context from context.
	span := diagUtils.SpanFromContext(reqCtx)
	// Populate W3C traceparent to cloudevent envelope
	corID := diag.SpanContextToW3CString(span.SpanContext())
	// Populate W3C tracestate to cloudevent envelope
	traceState := diag.TraceStateToW3CString(span.SpanContext())

	data := body

	if !rawPayload {
		envelope, err := runtimePubsub.NewCloudEvent(&runtimePubsub.CloudEvent{
			Source:          a.id,
			Topic:           topic,
			DataContentType: contentType,
			Data:            body,
			TraceID:         corID,
			TraceState:      traceState,
			Pubsub:          pubsubName,
		}, metadata)
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

		if errors.As(err, &runtimePubsub.NotAllowedError{}) {
			msg = NewErrorResponse("ERR_PUBSUB_FORBIDDEN", err.Error())
			status = fasthttp.StatusForbidden
		}

		if errors.As(err, &runtimePubsub.NotFoundError{}) {
			msg = NewErrorResponse("ERR_PUBSUB_NOT_FOUND", err.Error())
			status = fasthttp.StatusBadRequest
		}

		respond(reqCtx, withError(status, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withEmpty())
	}
}

type bulkPublishMessageEntry struct {
	EntryId     string            `json:"entryId,omitempty"` //nolint:stylecheck
	Event       interface{}       `json:"event"`
	ContentType string            `json:"contentType"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func (a *api) onBulkPublish(reqCtx *fasthttp.RequestCtx) {
	thepubsub, pubsubName, topic, sc, errRes := a.validateAndGetPubsubAndTopic(reqCtx)
	if errRes != nil {
		respond(reqCtx, withError(sc, *errRes))

		return
	}

	body := reqCtx.PostBody()
	metadata := getMetadataFromRequest(reqCtx)
	rawPayload, metaErr := contribMetadata.IsRawPayload(metadata)
	if metaErr != nil {
		msg := NewErrorResponse("ERR_PUBSUB_REQUEST_METADATA",
			fmt.Sprintf(messages.ErrMetadataGet, metaErr.Error()))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)

		return
	}

	// Extract trace context from context.
	span := diagUtils.SpanFromContext(reqCtx)
	// Populate W3C tracestate to cloudevent envelope
	traceState := diag.TraceStateToW3CString(span.SpanContext())

	incomingEntries := make([]bulkPublishMessageEntry, 0)
	err := json.Unmarshal(body, &incomingEntries)
	if err != nil {
		msg := NewErrorResponse("ERR_PUBSUB_EVENTS_SER",
			fmt.Sprintf(messages.ErrPubsubUnmarshal, topic, pubsubName, err.Error()))
		respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)

		return
	}
	entries := make([]pubsub.BulkMessageEntry, len(incomingEntries))

	entryIdSet := map[string]struct{}{} //nolint:stylecheck

	for i, entry := range incomingEntries {
		var dBytes []byte

		dBytes, cErr := ConvertEventToBytes(entry.Event, entry.ContentType)
		if cErr != nil {
			msg := NewErrorResponse("ERR_PUBSUB_EVENTS_SER",
				fmt.Sprintf(messages.ErrPubsubMarshal, topic, pubsubName, cErr.Error()))
			respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(msg)
			return
		}
		entries[i] = pubsub.BulkMessageEntry{
			Event:       dBytes,
			ContentType: entry.ContentType,
		}
		if entry.Metadata != nil {
			// Populate entry metadata with request level metadata. Entry level metadata keys
			// override request level metadata.
			entries[i].Metadata = utils.PopulateMetadataForBulkPublishEntry(metadata, entry.Metadata)
		}
		if _, ok := entryIdSet[entry.EntryId]; ok || entry.EntryId == "" {
			msg := NewErrorResponse("ERR_PUBSUB_EVENTS_SER",
				fmt.Sprintf(messages.ErrPubsubMarshal, topic, pubsubName, "error: entryId is duplicated or not present for entry"))
			respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(msg)

			return
		}
		entryIdSet[entry.EntryId] = struct{}{}
		entries[i].EntryId = entry.EntryId
	}

	spanMap := map[int]trace.Span{}
	// closeChildSpans method is called on every respond() call in all return paths in the following block of code.
	closeChildSpans := func(ctx *fasthttp.RequestCtx) {
		for _, span := range spanMap {
			diag.UpdateSpanStatusFromHTTPStatus(span, ctx.Response.StatusCode())
			span.End()
		}
	}
	features := thepubsub.Features()
	if !rawPayload {
		for i := range entries {
			// For multiple events in a single bulk call traceParent is different for each event.
			childSpan := diag.StartProducerSpanChildFromParent(reqCtx, span)
			// Populate W3C traceparent to cloudevent envelope
			corID := diag.SpanContextToW3CString(childSpan.SpanContext())
			spanMap[i] = childSpan

			envelope, envelopeErr := runtimePubsub.NewCloudEvent(&runtimePubsub.CloudEvent{
				Source:          a.id,
				Topic:           topic,
				DataContentType: entries[i].ContentType,
				Data:            entries[i].Event,
				TraceID:         corID,
				TraceState:      traceState,
				Pubsub:          pubsubName,
			}, entries[i].Metadata)
			if envelopeErr != nil {
				msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER",
					fmt.Sprintf(messages.ErrPubsubCloudEventCreation, err.Error()))
				respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg), closeChildSpans)
				log.Debug(msg)

				return
			}

			pubsub.ApplyMetadata(envelope, features, entries[i].Metadata)

			entries[i].Event, err = json.Marshal(envelope)
			if err != nil {
				msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER",
					fmt.Sprintf(messages.ErrPubsubCloudEventsSer, topic, pubsubName, err.Error()))
				respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg), closeChildSpans)
				log.Debug(msg)

				return
			}
		}
	}

	req := pubsub.BulkPublishRequest{
		PubsubName: pubsubName,
		Topic:      topic,
		Entries:    entries,
		Metadata:   metadata,
	}

	start := time.Now()
	res, err := a.pubsubAdapter.BulkPublish(&req)
	elapsed := diag.ElapsedSince(start)

	// BulkPublishResponse contains all failed entries from the request.
	// If there are no errors, then an empty response is returned.
	bulkRes := BulkPublishResponse{}
	eventsPublished := int64(len(req.Entries))
	if len(res.FailedEntries) != 0 {
		eventsPublished -= int64(len(res.FailedEntries))
	}

	diag.DefaultComponentMonitoring.BulkPubsubEgressEvent(context.Background(), pubsubName, topic, err == nil, eventsPublished, elapsed)

	if err != nil {
		bulkRes.FailedEntries = make([]BulkPublishResponseFailedEntry, 0, len(res.FailedEntries))
		for _, r := range res.FailedEntries {
			resEntry := BulkPublishResponseFailedEntry{EntryId: r.EntryId}
			if r.Error != nil {
				resEntry.Error = r.Error.Error()
			}
			bulkRes.FailedEntries = append(bulkRes.FailedEntries, resEntry)
		}
		status := fasthttp.StatusInternalServerError
		bulkRes.ErrorCode = "ERR_PUBSUB_PUBLISH_MESSAGE"

		if errors.As(err, &runtimePubsub.NotAllowedError{}) {
			msg := NewErrorResponse("ERR_PUBSUB_FORBIDDEN", err.Error())
			status = fasthttp.StatusForbidden
			respond(reqCtx, withError(status, msg), closeChildSpans)
			log.Debug(msg)

			return
		}

		if errors.As(err, &runtimePubsub.NotFoundError{}) {
			msg := NewErrorResponse("ERR_PUBSUB_NOT_FOUND", err.Error())
			status = fasthttp.StatusBadRequest
			respond(reqCtx, withError(status, msg), closeChildSpans)
			log.Debug(msg)

			return
		}

		// Return the error along with the list of failed entries.
		resData, _ := json.Marshal(bulkRes)
		respond(reqCtx, withJSON(status, resData), closeChildSpans)
		return
	}

	// If there are no errors, then an empty response is returned.
	respond(reqCtx, withEmpty(), closeChildSpans)
}

// validateAndGetPubsubAndTopic takes input as request context and returns the pubsub interface, pubsub name, topic name,
// or error status code and an ErrorResponse object.
func (a *api) validateAndGetPubsubAndTopic(reqCtx *fasthttp.RequestCtx) (pubsub.PubSub, string, string, int, *ErrorResponse) {
	if a.pubsubAdapter == nil {
		msg := NewErrorResponse("ERR_PUBSUB_NOT_CONFIGURED", messages.ErrPubsubNotConfigured)

		return nil, "", "", fasthttp.StatusBadRequest, &msg
	}

	pubsubName := reqCtx.UserValue(pubsubnameparam).(string)
	if pubsubName == "" {
		msg := NewErrorResponse("ERR_PUBSUB_EMPTY", messages.ErrPubsubEmpty)

		return nil, "", "", fasthttp.StatusNotFound, &msg
	}

	thepubsub := a.pubsubAdapter.GetPubSub(pubsubName)
	if thepubsub == nil {
		msg := NewErrorResponse("ERR_PUBSUB_NOT_FOUND", fmt.Sprintf(messages.ErrPubsubNotFound, pubsubName))

		return nil, "", "", fasthttp.StatusNotFound, &msg
	}

	topic := reqCtx.UserValue(topicParam).(string)
	if topic == "" {
		msg := NewErrorResponse("ERR_TOPIC_EMPTY", fmt.Sprintf(messages.ErrTopicEmpty, pubsubName))

		return nil, "", "", fasthttp.StatusNotFound, &msg
	}
	return thepubsub, pubsubName, topic, fasthttp.StatusOK, nil
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
		msg := NewErrorResponse("ERR_OUTBOUND_HEALTH_NOT_READY", messages.ErrOutboundHealthNotReady)
		respond(reqCtx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(reqCtx, withEmpty())
	}
}

func getMetadataFromRequest(reqCtx *fasthttp.RequestCtx) map[string]string {
	metadata := map[string]string{}
	prefixBytes := []byte(metadataPrefix)
	reqCtx.QueryArgs().VisitAll(func(key []byte, value []byte) {
		if bytes.HasPrefix(key, prefixBytes) {
			k := string(key[len(prefixBytes):])
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

	operations := make([]state.TransactionalStateOperation, len(req.Operations))
	for i, o := range req.Operations {
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
			upsertReq.Key, err = stateLoader.GetModifiedStateKey(upsertReq.Key, storeName, a.id)
			if err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
				respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
				log.Debug(err)
				return
			}
			operations[i] = state.TransactionalStateOperation{
				Request:   upsertReq,
				Operation: state.Upsert,
			}
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
			delReq.Key, err = stateLoader.GetModifiedStateKey(delReq.Key, storeName, a.id)
			if err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
				respond(reqCtx, withError(fasthttp.StatusBadRequest, msg))
				log.Debug(msg)
				return
			}
			operations[i] = state.TransactionalStateOperation{
				Request:   delReq,
				Operation: state.Delete,
			}
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
	policyRunner := resiliency.NewRunner[any](reqCtx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	storeReq := &state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   req.Metadata,
	}
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, transactionalStore.Multi(reqCtx, storeReq)
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
	policyRunner := resiliency.NewRunner[*state.QueryResponse](reqCtx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	resp, err := policyRunner(func(ctx context.Context) (*state.QueryResponse, error) {
		return querier.Query(ctx, &req)
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
		qresp.Results[i].Key = stateLoader.GetOriginalStateKey(resp.Results[i].Key)
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
