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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mitchellh/mapstructure"
	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	apierrors "github.com/dapr/dapr/pkg/api/errors"
	"github.com/dapr/dapr/pkg/api/http/endpoints"
	"github.com/dapr/dapr/pkg/api/universal"
	"github.com/dapr/dapr/pkg/channel/http"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/utils"
	kiterrors "github.com/dapr/kit/errors"
)

// API returns a list of HTTP endpoints for Dapr.
type API interface {
	APIEndpoints() []endpoints.Endpoint
	PublicEndpoints() []endpoints.Endpoint
	MarkStatusAsReady()
	MarkStatusAsOutboundReady()
}

type api struct {
	universal             *universal.Universal
	endpoints             []endpoints.Endpoint
	publicEndpoints       []endpoints.Endpoint
	directMessaging       invokev1.DirectMessaging
	channels              *channels.Channels
	pubsubAdapter         runtimePubsub.Adapter
	sendToOutputBindingFn func(ctx context.Context, name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	readyStatus           bool
	outboundReadyStatus   bool
	tracingSpec           config.TracingSpec
	maxRequestBodySize    int64 // In bytes
}

const (
	apiVersionV1             = "v1.0"
	apiVersionV1alpha1       = "v1.0-alpha1"
	apiVersionV1beta1        = "v1.0-beta1"
	methodParam              = "method"
	wildcardParam            = "*"
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
	Universal             *universal.Universal
	Channels              *channels.Channels
	DirectMessaging       invokev1.DirectMessaging
	PubsubAdapter         runtimePubsub.Adapter
	SendToOutputBindingFn func(ctx context.Context, name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	TracingSpec           config.TracingSpec
	MaxRequestBodySize    int64 // In bytes
}

// NewAPI returns a new API.
func NewAPI(opts APIOpts) API {
	api := &api{
		universal:             opts.Universal,
		channels:              opts.Channels,
		directMessaging:       opts.DirectMessaging,
		pubsubAdapter:         opts.PubsubAdapter,
		sendToOutputBindingFn: opts.SendToOutputBindingFn,
		tracingSpec:           opts.TracingSpec,
		maxRequestBodySize:    opts.MaxRequestBodySize,
	}

	metadataEndpoints := api.constructMetadataEndpoints()
	healthEndpoints := api.constructHealthzEndpoints()

	api.endpoints = append(api.endpoints, api.constructStateEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructSecretsEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructPubSubEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructActorEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructDirectMessagingEndpoints()...)
	api.endpoints = append(api.endpoints, metadataEndpoints...)
	api.endpoints = append(api.endpoints, api.constructShutdownEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructBindingsEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructConfigurationEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructSubtleCryptoEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructCryptoEndpoints()...)
	api.endpoints = append(api.endpoints, healthEndpoints...)
	api.endpoints = append(api.endpoints, api.constructDistributedLockEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructWorkflowEndpoints()...)

	api.publicEndpoints = append(api.publicEndpoints, metadataEndpoints...)
	api.publicEndpoints = append(api.publicEndpoints, healthEndpoints...)

	return api
}

// APIEndpoints returns the list of registered endpoints.
func (a *api) APIEndpoints() []endpoints.Endpoint {
	return a.endpoints
}

// PublicEndpoints returns the list of registered endpoints.
func (a *api) PublicEndpoints() []endpoints.Endpoint {
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

var endpointGroupStateV1 = &endpoints.EndpointGroup{
	Name:                 endpoints.EndpointGroupState,
	Version:              endpoints.EndpointGroupVersion1,
	AppendSpanAttributes: appendStateSpanAttributes,
}

func appendStateSpanAttributes(r *nethttp.Request, m map[string]string) {
	m[diagConsts.DBSystemSpanAttributeKey] = diagConsts.StateBuildingBlockType
	m[diagConsts.DBConnectionStringSpanAttributeKey] = diagConsts.StateBuildingBlockType
	m[diagConsts.DBStatementSpanAttributeKey] = r.Method + " " + r.URL.Path
	m[diagConsts.DBNameSpanAttributeKey] = chi.URLParam(r, storeNameParam)
}

func (a *api) constructStateEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "state/{storeName}/{key}",
			Version:         apiVersionV1,
			Group:           endpointGroupStateV1,
			FastHTTPHandler: a.onGetState,
			Settings: endpoints.EndpointSettings{
				Name: "GetState",
			},
		},
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "state/{storeName}",
			Version:         apiVersionV1,
			Group:           endpointGroupStateV1,
			FastHTTPHandler: a.onPostState,
			Settings: endpoints.EndpointSettings{
				Name: "SaveState",
			},
		},
		{
			Methods:         []string{nethttp.MethodDelete},
			Route:           "state/{storeName}/{key}",
			Version:         apiVersionV1,
			Group:           endpointGroupStateV1,
			FastHTTPHandler: a.onDeleteState,
			Settings: endpoints.EndpointSettings{
				Name: "DeleteState",
			},
		},
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "state/{storeName}/bulk",
			Version:         apiVersionV1,
			Group:           endpointGroupStateV1,
			FastHTTPHandler: a.onBulkGetState,
			Settings: endpoints.EndpointSettings{
				Name: "GetBulkState",
			},
		},
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "state/{storeName}/transaction",
			Version:         apiVersionV1,
			Group:           endpointGroupStateV1,
			FastHTTPHandler: a.onPostStateTransaction,
			Settings: endpoints.EndpointSettings{
				Name: "ExecuteStateTransaction",
			},
		},
		{
			Methods: []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:   "state/{storeName}/query",
			Version: apiVersionV1alpha1,
			Group: &endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupState,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: appendStateSpanAttributes,
			},
			Handler: a.onQueryStateHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "QueryStateAlpha1",
			},
		},
	}
}

func appendPubSubSpanAttributes(r *nethttp.Request, m map[string]string) {
	m[diagConsts.MessagingSystemSpanAttributeKey] = "pubsub"
	m[diagConsts.MessagingDestinationSpanAttributeKey] = chi.URLParam(r, "topic")
	m[diagConsts.MessagingDestinationKindSpanAttributeKey] = diagConsts.MessagingDestinationTopicKind
}

func (a *api) constructPubSubEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:   "publish/{pubsubname}/*",
			Version: apiVersionV1,
			Group: &endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupPubsub,
				Version:              endpoints.EndpointGroupVersion1,
				AppendSpanAttributes: appendPubSubSpanAttributes,
			},
			FastHTTPHandler: a.onPublish,
			Settings: endpoints.EndpointSettings{
				Name: "PublishEvent",
			},
		},
		{
			Methods: []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:   "publish/bulk/{pubsubname}/*",
			Version: apiVersionV1alpha1,
			Group: &endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupPubsub,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: appendPubSubSpanAttributes,
			},
			FastHTTPHandler: a.onBulkPublish,
			Settings: endpoints.EndpointSettings{
				Name: "BulkPublishEvent",
			},
		},
	}
}

func appendBindingsSpanAttributes(r *nethttp.Request, m map[string]string) {
	m[diagConsts.DBSystemSpanAttributeKey] = diagConsts.BindingBuildingBlockType
	m[diagConsts.DBConnectionStringSpanAttributeKey] = diagConsts.BindingBuildingBlockType
	m[diagConsts.DBStatementSpanAttributeKey] = r.Method + " " + r.URL.Path
	m[diagConsts.DBNameSpanAttributeKey] = chi.URLParam(r, nameParam)
}

func (a *api) constructBindingsEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:   "bindings/{name}",
			Version: apiVersionV1,
			Group: &endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupBindings,
				Version:              endpoints.EndpointGroupVersion1,
				AppendSpanAttributes: appendBindingsSpanAttributes,
			},
			FastHTTPHandler: a.onOutputBindingMessage,
			Settings: endpoints.EndpointSettings{
				Name: "InvokeBinding",
			},
		},
	}
}

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

var endpointGroupConfigurationV1Alpha1 = &endpoints.EndpointGroup{
	Name:                 endpoints.EndpointGroupConfiguration,
	Version:              endpoints.EndpointGroupVersion1alpha1,
	AppendSpanAttributes: nil, // TODO
}

var endpointGroupConfigurationV1 = &endpoints.EndpointGroup{
	Name:                 endpoints.EndpointGroupConfiguration,
	Version:              endpoints.EndpointGroupVersion1,
	AppendSpanAttributes: nil, // TODO
}

func (a *api) constructConfigurationEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "configuration/{storeName}",
			Version:         apiVersionV1alpha1,
			Group:           endpointGroupConfigurationV1Alpha1,
			FastHTTPHandler: a.onGetConfiguration,
			Settings: endpoints.EndpointSettings{
				Name: "GetConfiguration",
			},
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "configuration/{storeName}",
			Version:         apiVersionV1,
			Group:           endpointGroupConfigurationV1,
			FastHTTPHandler: a.onGetConfiguration,
			Settings: endpoints.EndpointSettings{
				Name: "GetConfiguration",
			},
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "configuration/{storeName}/subscribe",
			Version:         apiVersionV1alpha1,
			Group:           endpointGroupConfigurationV1Alpha1,
			FastHTTPHandler: a.onSubscribeConfiguration,
			Settings: endpoints.EndpointSettings{
				Name: "SubscribeConfiguration",
			},
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "configuration/{storeName}/subscribe",
			Version:         apiVersionV1,
			Group:           endpointGroupConfigurationV1,
			FastHTTPHandler: a.onSubscribeConfiguration,
			Settings: endpoints.EndpointSettings{
				Name: "SubscribeConfiguration",
			},
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "configuration/{storeName}/{configurationSubscribeID}/unsubscribe",
			Version:         apiVersionV1alpha1,
			Group:           endpointGroupConfigurationV1Alpha1,
			FastHTTPHandler: a.onUnsubscribeConfiguration,
			Settings: endpoints.EndpointSettings{
				Name: "UnsubscribeConfiguration",
			},
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "configuration/{storeName}/{configurationSubscribeID}/unsubscribe",
			Version:         apiVersionV1,
			Group:           endpointGroupConfigurationV1,
			FastHTTPHandler: a.onUnsubscribeConfiguration,
			Settings: endpoints.EndpointSettings{
				Name: "UnsubscribeConfiguration",
			},
		},
	}
}

func (a *api) onOutputBindingMessage(reqCtx *fasthttp.RequestCtx) {
	name := reqCtx.UserValue(nameParam).(string)
	body := reqCtx.PostBody()

	var req OutputBindingRequest
	err := json.Unmarshal(body, &req)
	if err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		universalFastHTTPErrorResponder(reqCtx, msg)
		log.Debug(msg)
		return
	}

	b, err := json.Marshal(req.Data)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST_DATA", fmt.Sprintf(messages.ErrMalformedRequestData, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
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
		if sc.TraceState().Len() > 0 {
			req.Metadata[tracestateHeader] = diag.TraceStateToW3CString(sc)
		}
	}

	start := time.Now()
	resp, err := a.sendToOutputBindingFn(reqCtx, name, &bindings.InvokeRequest{
		Metadata:  req.Metadata,
		Data:      b,
		Operation: bindings.OperationKind(req.Operation),
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.OutputBindingEvent(context.Background(), name, req.Operation, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", fmt.Sprintf(messages.ErrInvokeOutputBinding, name, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if resp == nil {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	} else {
		for k, v := range resp.Metadata {
			reqCtx.Response.Header.Add(metadataPrefix+k, v)
		}
		fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, resp.Data, resp.Metadata))
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
		msg := messages.ErrMalformedRequest.WithFormat(err)
		universalFastHTTPErrorResponder(reqCtx, msg)
		log.Debug(msg)
		return
	}

	// merge metadata from URL query parameters
	metadata := getMetadataFromFastHTTPRequest(reqCtx)
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
		fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, b, nil))
		return
	}

	var key string
	reqs := make([]state.GetRequest, len(req.Keys))
	for i, k := range req.Keys {
		key, err = stateLoader.GetModifiedStateKey(k, storeName, a.universal.AppID())
		if err != nil {
			status := apierrors.StateStoreInvalidKeyName(storeName, k, err.Error())
			universalFastHTTPErrorResponder(reqCtx, status)
			log.Debug(status)
			return
		}
		r := state.GetRequest{
			Key:      key,
			Metadata: req.Metadata,
		}
		reqs[i] = r
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[[]state.BulkGetResponse](reqCtx,
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	responses, err := policyRunner(func(ctx context.Context) ([]state.BulkGetResponse, error) {
		return store.BulkGet(ctx, reqs, state.BulkGetOpts{
			Parallelism: req.Parallelism,
		})
	})

	elapsed := diag.ElapsedSince(start)
	diag.DefaultComponentMonitoring.StateInvoked(context.Background(), storeName, diag.BulkGet, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_STATE_BULK_GET", err.Error())
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	for i := 0; i < len(responses) && i < len(req.Keys); i++ {
		bulkResp[i].Key = stateLoader.GetOriginalStateKey(responses[i].Key)
		if responses[i].Error != "" {
			log.Debugf("bulk get: error getting key %s: %s", bulkResp[i].Key, responses[i].Error)
			bulkResp[i].Error = responses[i].Error
		} else {
			bulkResp[i].Data = json.RawMessage(responses[i].Data)
			bulkResp[i].ETag = responses[i].ETag
			bulkResp[i].Metadata = responses[i].Metadata
		}
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
	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, b, nil))
}

func (a *api) getStateStoreWithRequestValidation(reqCtx *fasthttp.RequestCtx) (state.Store, string, error) {
	storeName := a.getStateStoreName(reqCtx)

	if a.universal.CompStore().StateStoresLen() == 0 {
		err := apierrors.NotConfigured(storeName, string(contribMetadata.StateStoreType)+" store", map[string]string{"appID": a.universal.AppID()}, codes.FailedPrecondition, nethttp.StatusInternalServerError, "ERR_STATE_STORE_NOT_CONFIGURED", kiterrors.CodePrefixStateStore+kiterrors.CodeNotConfigured)
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return nil, "", err
	}

	stateStore, ok := a.universal.CompStore().GetStateStore(storeName)
	if !ok {
		err := apierrors.NotFound(storeName, string(contribMetadata.StateStoreType)+" store", map[string]string{"appID": a.universal.AppID()}, codes.InvalidArgument, nethttp.StatusBadRequest, "ERR_STATE_STORE_NOT_FOUND", kiterrors.CodePrefixStateStore+kiterrors.CodeNotFound)
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return nil, "", err
	}
	return stateStore, storeName, nil
}

func (a *api) onGetState(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getStateStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromFastHTTPRequest(reqCtx)

	key := reqCtx.UserValue(stateKeyParam).(string)
	consistency := string(reqCtx.QueryArgs().Peek(consistencyParam))
	k, err := stateLoader.GetModifiedStateKey(key, storeName, a.universal.AppID())
	if err != nil {
		status := apierrors.StateStoreInvalidKeyName(storeName, key, err.Error())
		universalFastHTTPErrorResponder(reqCtx, status)
		log.Debug(status)

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
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	resp, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
		return store.Get(ctx, req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(context.Background(), storeName, diag.Get, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_STATE_GET", fmt.Sprintf(messages.ErrStateGet, key, storeName, err.Error()))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if resp == nil || resp.Data == nil {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
		return
	}

	if encryption.EncryptedStateStore(storeName) {
		val, err := encryption.TryDecryptValue(storeName, resp.Data)
		if err != nil {
			msg := NewErrorResponse("ERR_STATE_GET", fmt.Sprintf(messages.ErrStateGet, key, storeName, err.Error()))
			fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
			log.Debug(msg)
			return
		}

		resp.Data = val
	}

	if resp.ETag != nil {
		reqCtx.Response.Header.Add(etagHeader, *resp.ETag)
	}

	for k, v := range resp.Metadata {
		reqCtx.Response.Header.Add(metadataPrefix+k, v)
	}

	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, resp.Data, resp.Metadata))
}

func (a *api) getConfigurationStoreWithRequestValidation(reqCtx *fasthttp.RequestCtx) (configuration.Store, string, error) {
	if a.universal.CompStore().ConfigurationsLen() == 0 {
		msg := NewErrorResponse("ERR_CONFIGURATION_STORE_NOT_CONFIGURED", messages.ErrConfigurationStoresNotConfigured)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}

	storeName := a.getStateStoreName(reqCtx)

	conf, ok := a.universal.CompStore().GetConfiguration(storeName)
	if !ok {
		msg := NewErrorResponse("ERR_CONFIGURATION_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrConfigurationStoreNotFound, storeName))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}
	return conf, storeName, nil
}

type subscribeConfigurationResponse struct {
	ID string `json:"id"`
}

type UnsubscribeConfigurationResponse struct {
	Ok      bool   `json:"ok,omitempty"      protobuf:"varint,1,opt,name=ok,proto3"`
	Message string `json:"message,omitempty" protobuf:"bytes,2,opt,name=message,proto3"`
}

type configurationEventHandler struct {
	api       *api
	storeName string
	channels  *channels.Channels
	res       resiliency.Provider
}

func (h *configurationEventHandler) updateEventHandler(ctx context.Context, e *configuration.UpdateEvent) error {
	appChannel := h.channels.AppChannel()
	if appChannel == nil {
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

		policyRunner := resiliency.NewRunner[struct{}](ctx, policyDef)
		_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
			rResp, rErr := appChannel.InvokeMethod(ctx, req, "")
			if rErr != nil {
				return struct{}{}, rErr
			}
			if rResp != nil {
				defer rResp.Close()
			}

			if rResp != nil && rResp.Status().GetCode() != nethttp.StatusOK {
				return struct{}{}, fmt.Errorf("error sending configuration item to application, status %d", rResp.Status().GetCode())
			}
			return struct{}{}, nil
		})
		if err != nil {
			log.Errorf("error sending configuration item to the app: %v", err)
		}
	}
	return nil
}

func (a *api) onSubscribeConfiguration(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getConfigurationStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}
	if a.channels.AppChannel() == nil {
		msg := NewErrorResponse("ERR_APP_CHANNEL_NIL", "app channel is not initialized. cannot subscribe to configuration updates")
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	metadata := getMetadataFromFastHTTPRequest(reqCtx)
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
		api:       a,
		storeName: storeName,
		channels:  a.channels,
		res:       a.universal.Resiliency(),
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[string](reqCtx,
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Configuration),
	)
	subscribeID, err := policyRunner(func(ctx context.Context) (string, error) {
		return store.Subscribe(ctx, req, handler.updateEventHandler)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), storeName, diag.ConfigurationSubscribe, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_CONFIGURATION_SUBSCRIBE", fmt.Sprintf(messages.ErrConfigurationSubscribe, keys, storeName, err.Error()))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	respBytes, _ := json.Marshal(&subscribeConfigurationResponse{
		ID: subscribeID,
	})
	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, respBytes, nil))
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
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Configuration),
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
		fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusInternalServerError, errRespBytes, nil))
		log.Debug(msg)
		return
	}
	respBytes, _ := json.Marshal(&UnsubscribeConfigurationResponse{
		Ok: true,
	})
	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, respBytes, nil))
}

func (a *api) onGetConfiguration(reqCtx *fasthttp.RequestCtx) {
	store, storeName, err := a.getConfigurationStoreWithRequestValidation(reqCtx)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromFastHTTPRequest(reqCtx)

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
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Configuration),
	)
	getResponse, err := policyRunner(func(ctx context.Context) (*configuration.GetResponse, error) {
		return store.Get(ctx, req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), storeName, diag.Get, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_CONFIGURATION_GET", fmt.Sprintf(messages.ErrConfigurationGet, keys, storeName, err.Error()))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if getResponse == nil || getResponse.Items == nil || len(getResponse.Items) == 0 {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
		return
	}

	respBytes, _ := json.Marshal(getResponse.Items)

	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, respBytes, nil))
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

	metadata := getMetadataFromFastHTTPRequest(reqCtx)
	k, err := stateLoader.GetModifiedStateKey(key, storeName, a.universal.AppID())
	if err != nil {
		status := apierrors.StateStoreInvalidKeyName(storeName, key, err.Error())
		universalFastHTTPErrorResponder(reqCtx, status)
		log.Debug(status)
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
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.Delete(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(reqCtx, storeName, diag.Delete, err == nil, elapsed)

	if err != nil {
		statusCode, errMsg, resp := a.stateErrorResponse(err, "ERR_STATE_DELETE")
		resp.Message = fmt.Sprintf(messages.ErrStateDelete, key, errMsg)

		fasthttpRespond(reqCtx, fasthttpResponseWithError(statusCode, resp))
		log.Debug(resp.Message)
		return
	}
	fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
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
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}
	if len(reqs) == 0 {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
		return
	}

	metadata := getMetadataFromFastHTTPRequest(reqCtx)

	for i, r := range reqs {
		if len(reqs[i].Key) == 0 {
			msg := NewErrorResponse("ERR_MALFORMED_REQUEST", `"key" is a required field`)
			fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
			log.Debug(msg)
			return
		}

		// merge metadata from URL query parameters
		if reqs[i].Metadata == nil {
			reqs[i].Metadata = metadata
		} else {
			for k, v := range metadata {
				reqs[i].Metadata[k] = v
			}
		}

		reqs[i].Key, err = stateLoader.GetModifiedStateKey(r.Key, storeName, a.universal.AppID())
		if err != nil {
			status := apierrors.StateStoreInvalidKeyName(storeName, r.Key, err.Error())
			universalFastHTTPErrorResponder(reqCtx, status)
			log.Debug(status)
			return
		}

		if encryption.EncryptedStateStore(storeName) {
			data := []byte(fmt.Sprintf("%v", r.Value))
			val, encErr := encryption.TryEncryptValue(storeName, data)
			if encErr != nil {
				statusCode, errMsg, resp := a.stateErrorResponse(encErr, "ERR_STATE_SAVE")
				resp.Message = fmt.Sprintf(messages.ErrStateSave, storeName, errMsg)

				fasthttpRespond(reqCtx, fasthttpResponseWithError(statusCode, resp))
				log.Debug(resp.Message)
				return
			}

			reqs[i].Value = val
		}
	}

	start := time.Now()
	err = stateLoader.PerformBulkStoreOperation(reqCtx, reqs,
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Statestore),
		state.BulkStoreOpts{},
		store.Set,
		store.BulkSet,
	)
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(reqCtx, storeName, diag.Set, err == nil, elapsed)

	if err != nil {
		statusCode, errMsg, resp := a.stateErrorResponse(err, "ERR_STATE_SAVE")
		resp.Message = fmt.Sprintf(messages.ErrStateSave, storeName, errMsg)

		fasthttpRespond(reqCtx, fasthttpResponseWithError(statusCode, resp))
		log.Debug(resp.Message)
		return
	}

	fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
}

// stateErrorResponse takes a state store error and returns a corresponding status code, error message and modified user error.
func (a *api) stateErrorResponse(err error, errorCode string) (int, string, ErrorResponse) {
	etag, code, message := a.etagError(err)

	r := ErrorResponse{
		ErrorCode: errorCode,
	}
	if etag {
		return code, message, r
	}
	message = err.Error()

	return nethttp.StatusInternalServerError, message, r
}

// etagError checks if the error from the state store is an etag error and returns a bool for indication,
// an status code and an error message.
func (a *api) etagError(err error) (bool, int, string) {
	var etagErr *state.ETagError
	if errors.As(err, &etagErr) {
		switch etagErr.Kind() {
		case state.ETagMismatch:
			return true, nethttp.StatusConflict, etagErr.Error()
		case state.ETagInvalid:
			return true, nethttp.StatusBadRequest, etagErr.Error()
		}
	}
	return false, -1, ""
}

func (a *api) getStateStoreName(reqCtx *fasthttp.RequestCtx) string {
	return reqCtx.UserValue(storeNameParam).(string)
}

func (a *api) onCreateActorReminder(reqCtx *fasthttp.RequestCtx) {
	if !a.actorReadinessCheckFastHTTP(reqCtx) {
		// Response already sent
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

		msg := NewErrorResponse("ERR_ACTOR_REMINDER_CREATE", fmt.Sprintf(messages.ErrActorReminderCreate, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
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
		msg := NewErrorResponse("ERR_ACTOR_TIMER_CREATE", fmt.Sprintf(messages.ErrActorTimerCreate, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
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

		msg := NewErrorResponse("ERR_ACTOR_REMINDER_DELETE", fmt.Sprintf(messages.ErrActorReminderDelete, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
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
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	hosted := a.universal.Actors().IsActorHosted(reqCtx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", messages.ErrActorInstanceMissing)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
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
		msg := NewErrorResponse("ERR_ACTOR_STATE_TRANSACTION_SAVE", fmt.Sprintf(messages.ErrActorStateTransactionSave, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
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

		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", fmt.Sprintf(messages.ErrActorReminderGet, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	b, err := json.Marshal(resp)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", fmt.Sprintf(messages.ErrActorReminderGet, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, b, nil))
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
		msg := NewErrorResponse("ERR_ACTOR_TIMER_DELETE", fmt.Sprintf(messages.ErrActorTimerDelete, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
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
			msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, err))
			fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
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
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, "failed to cast response"))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
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
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", messages.ErrActorInstanceMissing)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req := actors.GetStateRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Key:       key,
	}

	resp, err := a.universal.Actors().GetState(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_GET", fmt.Sprintf(messages.ErrActorStateGet, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		if resp == nil || len(resp.Data) == 0 {
			fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
			return
		}
		fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, resp.Data, resp.Metadata))
	}
}

func (a *api) onPublish(reqCtx *fasthttp.RequestCtx) {
	thepubsub, pubsubName, topic, validationErr := a.validateAndGetPubsubAndTopic(reqCtx)

	if validationErr != nil {
		log.Debug(validationErr)
		universalFastHTTPErrorResponder(reqCtx, validationErr)
		return
	}

	body := reqCtx.PostBody()
	contentType := string(reqCtx.Request.Header.Peek("Content-Type"))
	metadata := getMetadataFromFastHTTPRequest(reqCtx)
	rawPayload, metaErr := contribMetadata.IsRawPayload(metadata)
	if metaErr != nil {
		err := apierrors.PubSubMetadataDeserialize(pubsubName, string(contribMetadata.PubSubType), metadata, metaErr)
		universalFastHTTPErrorResponder(reqCtx, err)
		log.Debug(err)
		return
	}

	data := body

	if !rawPayload {
		span := diagUtils.SpanFromContext(reqCtx)
		corID, traceState := diag.TraceIDAndStateFromSpan(span)
		envelope, err := runtimePubsub.NewCloudEvent(&runtimePubsub.CloudEvent{
			Source:          a.universal.AppID(),
			Topic:           topic,
			DataContentType: contentType,
			Data:            body,
			TraceID:         corID,
			TraceState:      traceState,
			Pubsub:          pubsubName,
		}, metadata)
		if err != nil {
			nerr := apierrors.PubSubCloudEventCreation(pubsubName, string(contribMetadata.PubSubType), map[string]string{"appID": a.universal.AppID(), "error": err.Error()})
			universalFastHTTPErrorResponder(reqCtx, nerr)
			log.Debug(nerr)
			return
		}

		features := thepubsub.Features()

		pubsub.ApplyMetadata(envelope, features, metadata)

		data, err = json.Marshal(envelope)
		if err != nil {
			nerr := apierrors.PubSubMarshalEnvelope(pubsubName, topic, string(contribMetadata.PubSubType), map[string]string{"appID": a.universal.AppID(), "error": err.Error()})
			universalFastHTTPErrorResponder(reqCtx, nerr)
			log.Debug(nerr)
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
	err := a.pubsubAdapter.Publish(reqCtx, &req)
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.PubsubEgressEvent(context.Background(), pubsubName, topic, err == nil, elapsed)

	if err != nil {
		var nerr error

		switch {
		case errors.As(err, &runtimePubsub.NotAllowedError{}):
			nerr = apierrors.PubSubPublishForbidden(pubsubName, string(contribMetadata.PubSubType), topic, a.universal.AppID(), err)
		case errors.As(err, &runtimePubsub.NotFoundError{}):
			nerr = apierrors.PubSubTestNotFound(pubsubName, string(contribMetadata.PubSubType), topic, err)
		default:
			nerr = apierrors.PubSubPublishMessage(pubsubName, string(contribMetadata.PubSubType), topic, err)
		}

		universalFastHTTPErrorResponder(reqCtx, nerr)
		log.Debug(nerr)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
}

type bulkPublishMessageEntry struct {
	EntryID     string            `json:"entryId,omitempty"`
	Event       interface{}       `json:"event"`
	ContentType string            `json:"contentType"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func (a *api) onBulkPublish(reqCtx *fasthttp.RequestCtx) {
	thepubsub, pubsubName, topic, validationErr := a.validateAndGetPubsubAndTopic(reqCtx)

	if validationErr != nil {
		log.Debug(validationErr)
		universalFastHTTPErrorResponder(reqCtx, validationErr)
		return
	}

	body := reqCtx.PostBody()
	metadata := getMetadataFromFastHTTPRequest(reqCtx)
	rawPayload, metaErr := contribMetadata.IsRawPayload(metadata)
	if metaErr != nil {
		err := apierrors.PubSubMetadataDeserialize(pubsubName, string(contribMetadata.PubSubType), metadata, metaErr)
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}

	// Extract trace context from context.
	span := diagUtils.SpanFromContext(reqCtx)

	incomingEntries := make([]bulkPublishMessageEntry, 0)
	err := json.Unmarshal(body, &incomingEntries)
	if err != nil {
		nerr := apierrors.PubSubUnMarshalEvents(pubsubName, string(contribMetadata.PubSubType), topic, map[string]string{"appID": a.universal.AppID()}, err)
		universalFastHTTPErrorResponder(reqCtx, nerr)
		log.Debug(nerr)
		return
	}
	entries := make([]pubsub.BulkMessageEntry, len(incomingEntries))

	entryIDSet := map[string]struct{}{}

	for i, entry := range incomingEntries {
		var dBytes []byte
		dBytes, err = ConvertEventToBytes(entry.Event, entry.ContentType)
		if err != nil {
			nerr := apierrors.PubSubMarshalEvents(pubsubName, string(contribMetadata.PubSubType), topic, map[string]string{"appID": a.universal.AppID(), "error": err.Error()})
			universalFastHTTPErrorResponder(reqCtx, nerr)
			log.Debug(nerr)
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
		if _, ok := entryIDSet[entry.EntryID]; ok || entry.EntryID == "" {
			nerr := apierrors.PubSubMarshalEvents(pubsubName, string(contribMetadata.PubSubType), topic, map[string]string{"appID": a.universal.AppID(), "error": "entryId is duplicated or not present for entry"})
			universalFastHTTPErrorResponder(reqCtx, nerr)
			log.Debug(nerr)
			return
		}
		entryIDSet[entry.EntryID] = struct{}{}
		entries[i].EntryId = entry.EntryID
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
			childSpan := diag.StartProducerSpanChildFromParent(reqCtx, span)
			corID, traceState := diag.TraceIDAndStateFromSpan(childSpan)
			// For multiple events in a single bulk call traceParent is different for each event.
			// Populate W3C traceparent to cloudevent envelope
			spanMap[i] = childSpan

			var envelope map[string]interface{}
			envelope, err = runtimePubsub.NewCloudEvent(&runtimePubsub.CloudEvent{
				Source:          a.universal.AppID(),
				Topic:           topic,
				DataContentType: entries[i].ContentType,
				Data:            entries[i].Event,
				TraceID:         corID,
				TraceState:      traceState,
				Pubsub:          pubsubName,
			}, entries[i].Metadata)
			if err != nil {
				nerr := apierrors.PubSubCloudEventCreation(pubsubName, string(contribMetadata.PubSubType), map[string]string{"appID": a.universal.AppID(), "error": err.Error()})
				standardizedErr, ok := kiterrors.FromError(nerr)
				if ok {
					fasthttpRespond(reqCtx, fasthttpResponseWithError(standardizedErr.HTTPStatusCode(), standardizedErr), closeChildSpans)
				}
				log.Debug(nerr)
				return
			}

			pubsub.ApplyMetadata(envelope, features, entries[i].Metadata)

			entries[i].Event, err = json.Marshal(envelope)
			if err != nil {
				nerr := apierrors.PubSubMarshalEnvelope(pubsubName, topic, string(contribMetadata.PubSubType), map[string]string{"appID": a.universal.AppID()})
				standardizedErr, ok := kiterrors.FromError(nerr)
				if ok {
					fasthttpRespond(reqCtx, fasthttpResponseWithError(standardizedErr.HTTPStatusCode(), standardizedErr), closeChildSpans)
				}
				log.Debug(nerr)
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
	res, err := a.pubsubAdapter.BulkPublish(reqCtx, &req)
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
		bulkRes.ErrorCode = "ERR_PUBSUB_PUBLISH_MESSAGE"

		switch {
		case errors.As(err, &runtimePubsub.NotAllowedError{}):
			nerr := apierrors.PubSubPublishForbidden(pubsubName, string(contribMetadata.PubSubType), topic, a.universal.AppID(), err)
			standardizedErr, ok := kiterrors.FromError(nerr)
			if ok {
				fasthttpRespond(reqCtx, fasthttpResponseWithError(standardizedErr.HTTPStatusCode(), standardizedErr), closeChildSpans)
			}
			log.Debug(nerr)
			return
		case errors.As(err, &runtimePubsub.NotFoundError{}):
			nerr := apierrors.PubSubTestNotFound(pubsubName, string(contribMetadata.PubSubType), topic, err)
			standardizedErr, ok := kiterrors.FromError(nerr)
			if ok {
				fasthttpRespond(reqCtx, fasthttpResponseWithError(standardizedErr.HTTPStatusCode(), standardizedErr), closeChildSpans)
			}
			return
		default:
			err = apierrors.PubSubPublishMessage(pubsubName, string(contribMetadata.PubSubType), topic, err)
			log.Debug(err)
		}

		// Return the error along with the list of failed entries.
		resData, _ := json.Marshal(bulkRes)
		if standardizedErr, ok := kiterrors.FromError(err); ok {
			fasthttpRespond(reqCtx, fasthttpResponseWithJSON(standardizedErr.HTTPStatusCode(), resData, map[string]string{"responseData": string(resData), "error": standardizedErr.Error()}), closeChildSpans)
		}
		return
	}

	// If there are no errors, then an empty response is returned.
	fasthttpRespond(reqCtx, fasthttpResponseWithEmpty(), closeChildSpans)
}

// validateAndGetPubsubAndTopic takes input as request context and returns the pubsub interface, pubsub name, topic name,
// or error status code and an ErrorResponse object.
func (a *api) validateAndGetPubsubAndTopic(reqCtx *fasthttp.RequestCtx) (pubsub.PubSub, string, string, error) {
	var err error
	pubsubName := reqCtx.UserValue(pubsubnameparam).(string)
	pubsubType := string(contribMetadata.PubSubType)
	metadata := getMetadataFromFastHTTPRequest(reqCtx)

	if a.pubsubAdapter == nil {
		err = apierrors.NotConfigured(pubsubName, pubsubType, metadata, codes.FailedPrecondition, nethttp.StatusBadRequest, "ERR_PUBSUB_NOT_CONFIGURED", kiterrors.CodePrefixPubSub+kiterrors.CodeNotConfigured)
		return nil, "", "", err
	}

	if pubsubName == "" {
		err = apierrors.PubSubNameEmpty(pubsubName, pubsubType, metadata)
		return nil, "", "", err
	}

	thepubsub, ok := a.universal.CompStore().GetPubSub(pubsubName)
	if !ok {
		err = apierrors.NotFound(pubsubName, pubsubType, metadata, codes.InvalidArgument, nethttp.StatusNotFound, "ERR_PUBSUB_NOT_FOUND", kiterrors.CodePrefixPubSub+kiterrors.CodeNotFound)
		return nil, "", "", err
	}

	topic := reqCtx.UserValue(wildcardParam).(string)
	if topic == "" {
		err = apierrors.PubSubTopicEmpty(pubsubName, pubsubType, metadata)
		return nil, "", "", err
	}

	return thepubsub.Component, pubsubName, topic, nil
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

	return nethttp.StatusOK
}

func getMetadataFromRequest(r *nethttp.Request) map[string]string {
	pl := len(metadataPrefix)
	qs := r.URL.Query()

	metadata := make(map[string]string, len(qs))
	for key, value := range qs {
		if !strings.HasPrefix(key, metadataPrefix) {
			continue
		}
		metadata[key[pl:]] = value[0]
	}

	return metadata
}

func getMetadataFromFastHTTPRequest(reqCtx *fasthttp.RequestCtx) map[string]string {
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

type stateTransactionRequestBody struct {
	Operations []stateTransactionRequestBodyOperation `json:"operations"`
	Metadata   map[string]string                      `json:"metadata,omitempty"`
}

type stateTransactionRequestBodyOperation struct {
	Operation string      `json:"operation"`
	Request   interface{} `json:"request"`
}

func (a *api) onPostStateTransaction(reqCtx *fasthttp.RequestCtx) {
	storeName := reqCtx.UserValue(storeNameParam).(string)

	if a.universal.CompStore().StateStoresLen() == 0 {
		err := apierrors.NotConfigured(storeName, string(contribMetadata.StateStoreType)+" store", map[string]string{"appID": a.universal.AppID()}, codes.FailedPrecondition, nethttp.StatusInternalServerError, "ERR_STATE_STORE_NOT_CONFIGURED", kiterrors.CodePrefixStateStore+kiterrors.CodeNotConfigured)
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}

	store, ok := a.universal.CompStore().GetStateStore(storeName)
	if !ok {
		err := apierrors.NotFound(storeName, string(contribMetadata.StateStoreType)+" store", map[string]string{"appID": a.universal.AppID()}, codes.InvalidArgument, nethttp.StatusBadRequest, "ERR_STATE_STORE_NOT_FOUND", kiterrors.CodePrefixStateStore+kiterrors.CodeNotFound)
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}

	transactionalStore, ok := store.(state.TransactionalStore)
	if !ok || !state.FeatureTransactional.IsPresent(store.Features()) {
		err := apierrors.StateStoreTransactionsNotSupported(storeName)
		universalFastHTTPErrorResponder(reqCtx, err)
		log.Debug(err)
		return
	}

	body := reqCtx.PostBody()
	var req stateTransactionRequestBody
	if err := json.Unmarshal(body, &req); err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		universalFastHTTPErrorResponder(reqCtx, msg)
		log.Debug(msg)
		return
	}
	if len(req.Operations) == 0 {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
		return
	}

	// merge metadata from URL query parameters
	metadata := getMetadataFromFastHTTPRequest(reqCtx)
	if req.Metadata == nil {
		req.Metadata = metadata
	} else {
		for k, v := range metadata {
			req.Metadata[k] = v
		}
	}

	operations := make([]state.TransactionalStateOperation, 0, len(req.Operations))
	for _, o := range req.Operations {
		switch o.Operation {
		case string(state.OperationUpsert):
			var upsertReq state.SetRequest
			err := mapstructure.Decode(o.Request, &upsertReq)
			if err != nil {
				msg := messages.ErrMalformedRequest.WithFormat(err)
				universalFastHTTPErrorResponder(reqCtx, msg)
				log.Debug(msg)
				return
			}
			upsertReq.Key, err = stateLoader.GetModifiedStateKey(upsertReq.Key, storeName, a.universal.AppID())
			if err != nil {
				status := apierrors.StateStoreInvalidKeyName(storeName, upsertReq.Key, err.Error())
				universalFastHTTPErrorResponder(reqCtx, status)
				log.Debug(status)
				return
			}
			operations = append(operations, upsertReq)
		case string(state.OperationDelete):
			var delReq state.DeleteRequest
			err := mapstructure.Decode(o.Request, &delReq)
			if err != nil {
				msg := messages.ErrMalformedRequest.WithFormat(err)
				universalFastHTTPErrorResponder(reqCtx, msg)
				log.Debug(msg)
				return
			}
			delReq.Key, err = stateLoader.GetModifiedStateKey(delReq.Key, storeName, a.universal.AppID())
			if err != nil {
				status := apierrors.StateStoreInvalidKeyName(storeName, delReq.Key, err.Error())
				universalFastHTTPErrorResponder(reqCtx, status)
				log.Debug(status)

				return
			}
			operations = append(operations, delReq)
		default:
			msg := NewErrorResponse(
				"ERR_NOT_SUPPORTED_STATE_OPERATION",
				fmt.Sprintf(messages.ErrNotSupportedStateOperation, o.Operation))
			fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
			log.Debug(msg)
			return
		}
	}

	if maxMulti, ok := store.(state.TransactionalStoreMultiMaxSize); ok {
		max := maxMulti.MultiMaxSize()
		if max > 0 && len(operations) > max {
			err := apierrors.StateStoreTooManyTransactionalOps(storeName, len(operations), max)
			log.Debug(err)
			universalFastHTTPErrorResponder(reqCtx, err)
			return
		}
	}

	if encryption.EncryptedStateStore(storeName) {
		for i, op := range operations {
			switch req := op.(type) {
			case state.SetRequest:
				data := []byte(fmt.Sprintf("%v", req.Value))
				val, err := encryption.TryEncryptValue(storeName, data)
				if err != nil {
					msg := NewErrorResponse(
						"ERR_SAVE_STATE",
						fmt.Sprintf(messages.ErrStateSave, storeName, err.Error()))
					fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
					log.Debug(msg)
					return
				}

				req.Value = val
				operations[i] = req
			}
		}
	}

	outboxEnabled := a.pubsubAdapter.Outbox().Enabled(storeName)
	if outboxEnabled {
		span := diagUtils.SpanFromContext(reqCtx)
		corID, traceState := diag.TraceIDAndStateFromSpan(span)
		trs, err := a.pubsubAdapter.Outbox().PublishInternal(reqCtx, storeName, operations, a.universal.AppID(), corID, traceState)
		if err != nil {
			nerr := apierrors.PubSubOubox(a.universal.AppID(), err)
			universalFastHTTPErrorResponder(reqCtx, nerr)
			log.Debug(nerr)
			return
		}

		operations = append(operations, trs...)
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[any](reqCtx,
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Statestore),
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
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
}

func (a *api) onQueryStateHandler() nethttp.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.QueryStateAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.QueryStateRequest, *runtimev1pb.QueryStateResponse]{
			// We pass the input body manually rather than parsing it using protojson
			SkipInputBody: true,
			InModifier: func(r *nethttp.Request, in *runtimev1pb.QueryStateRequest) (*runtimev1pb.QueryStateRequest, error) {
				in.StoreName = chi.URLParam(r, storeNameParam)
				in.Metadata = getMetadataFromRequest(r)

				body, err := io.ReadAll(r.Body)
				if err != nil {
					return nil, messages.ErrBodyRead.WithFormat(err)
				}
				in.Query = string(body)
				return in, nil
			},
			OutModifier: func(out *runtimev1pb.QueryStateResponse) (any, error) {
				// If the response is empty, return nil
				if out == nil || len(out.GetResults()) == 0 {
					return nil, nil
				}

				// We need to translate this to a JSON object because one of the fields must be returned as json.RawMessage
				qresp := &QueryResponse{
					Results:  make([]QueryItem, len(out.GetResults())),
					Token:    out.GetToken(),
					Metadata: out.GetMetadata(),
				}
				for i := range out.GetResults() {
					qresp.Results[i].Key = stateLoader.GetOriginalStateKey(out.GetResults()[i].GetKey())
					if out.GetResults()[i].GetEtag() != "" {
						qresp.Results[i].ETag = &out.Results[i].Etag
					}
					qresp.Results[i].Error = out.GetResults()[i].GetError()
					qresp.Results[i].Data = json.RawMessage(out.GetResults()[i].GetData())
				}
				return qresp, nil
			},
		},
	)
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
