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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mitchellh/mapstructure"
	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/channel/http"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
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
	"github.com/dapr/dapr/pkg/runtime/compstore"
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
	SetHTTPEndpointsAppChannel(appChannel channel.HTTPEndpointAppChannel)
	SetDirectMessaging(directMessaging messaging.DirectMessaging)
	SetActorRuntime(actor actors.Actors)
}

type api struct {
	universal               *universalapi.UniversalAPI
	endpoints               []Endpoint
	publicEndpoints         []Endpoint
	directMessaging         messaging.DirectMessaging
	appChannel              channel.AppChannel
	httpEndpointsAppChannel channel.HTTPEndpointAppChannel
	resiliency              resiliency.Provider
	pubsubAdapter           runtimePubsub.Adapter
	sendToOutputBindingFn   func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	readyStatus             bool
	outboundReadyStatus     bool
	tracingSpec             config.TracingSpec
	maxRequestBodySize      int64 // In bytes
}

const (
	apiVersionV1             = "v1.0"
	apiVersionV1alpha1       = "v1.0-alpha1"
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
	AppID                       string
	AppChannel                  channel.AppChannel
	HTTPEndpointsAppChannel     channel.HTTPEndpointAppChannel
	DirectMessaging             messaging.DirectMessaging
	Resiliency                  resiliency.Provider
	CompStore                   *compstore.ComponentStore
	PubsubAdapter               runtimePubsub.Adapter
	Actors                      actors.Actors
	SendToOutputBindingFn       func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	TracingSpec                 config.TracingSpec
	Shutdown                    func()
	GetComponentsCapabilitiesFn func() map[string][]string
	MaxRequestBodySize          int64 // In bytes
	AppConnectionConfig         config.AppConnectionConfig
	GlobalConfig                *config.Configuration
}

// NewAPI returns a new API.
func NewAPI(opts APIOpts) API {
	api := &api{
		appChannel:              opts.AppChannel,
		httpEndpointsAppChannel: opts.HTTPEndpointsAppChannel,
		directMessaging:         opts.DirectMessaging,
		resiliency:              opts.Resiliency,
		pubsubAdapter:           opts.PubsubAdapter,
		sendToOutputBindingFn:   opts.SendToOutputBindingFn,
		tracingSpec:             opts.TracingSpec,
		maxRequestBodySize:      opts.MaxRequestBodySize,
		universal: &universalapi.UniversalAPI{
			AppID:                      opts.AppID,
			Logger:                     log,
			Resiliency:                 opts.Resiliency,
			Actors:                     opts.Actors,
			CompStore:                  opts.CompStore,
			ShutdownFn:                 opts.Shutdown,
			GetComponentsCapabilitesFn: opts.GetComponentsCapabilitiesFn,
			AppConnectionConfig:        opts.AppConnectionConfig,
			GlobalConfig:               opts.GlobalConfig,
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
			Methods:         []string{nethttp.MethodGet},
			Route:           "workflows/{workflowComponent}/{instanceID}",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onGetWorkflowHandler(),
		},
		{
			Methods:         []string{nethttp.MethodPost},
			Route:           "workflows/{workflowComponent}/{instanceID}/raiseEvent/{eventName}",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onRaiseEventWorkflowHandler(),
		},
		{
			Methods:         []string{nethttp.MethodPost},
			Route:           "workflows/{workflowComponent}/{workflowName}/start",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onStartWorkflowHandler(),
		},
		{
			Methods:         []string{nethttp.MethodPost},
			Route:           "workflows/{workflowComponent}/{instanceID}/pause",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onPauseWorkflowHandler(),
		},
		{
			Methods:         []string{nethttp.MethodPost},
			Route:           "workflows/{workflowComponent}/{instanceID}/resume",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onResumeWorkflowHandler(),
		},
		{
			Methods:         []string{nethttp.MethodPost},
			Route:           "workflows/{workflowComponent}/{instanceID}/terminate",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onTerminateWorkflowHandler(),
		},
		{
			Methods:         []string{nethttp.MethodPost},
			Route:           "workflows/{workflowComponent}/{instanceID}/purge",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onPurgeWorkflowHandler(),
		},
	}
}

func (a *api) constructStateEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "state/{storeName}/{key}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onGetState,
		},
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "state/{storeName}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onPostState,
		},
		{
			Methods:         []string{nethttp.MethodDelete},
			Route:           "state/{storeName}/{key}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onDeleteState,
		},
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "state/{storeName}/bulk",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onBulkGetState,
		},
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "state/{storeName}/transaction",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onPostStateTransaction,
		},
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "state/{storeName}/query",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onQueryStateHandler(),
		},
	}
}

func (a *api) constructPubSubEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "publish/{pubsubname}/*",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onPublish,
		},
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "publish/bulk/{pubsubname}/*",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onBulkPublish,
		},
	}
}

func (a *api) constructBindingsEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "bindings/{name}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onOutputBindingMessage,
		},
	}
}

func (a *api) constructDirectMessagingEndpoints() []Endpoint {
	return []Endpoint{
		{
			// No method is defined here to match any method
			Methods: []string{},
			Route:   "invoke/*",
			// This is the fallback route for when no other method is matched by the router
			IsFallback:            true,
			Version:               apiVersionV1,
			KeepWildcardUnescaped: true,
			FastHTTPHandler:       a.onDirectMessage,
		},
	}
}

func (a *api) constructActorEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "actors/{actorType}/{actorId}/state",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onActorStateTransaction,
		},
		{
			Methods:         []string{nethttp.MethodGet, nethttp.MethodPost, nethttp.MethodDelete, nethttp.MethodPut},
			Route:           "actors/{actorType}/{actorId}/method/{method}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onDirectActorMessage,
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "actors/{actorType}/{actorId}/state/{key}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onGetActorState,
		},
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "actors/{actorType}/{actorId}/reminders/{name}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onCreateActorReminder,
		},
		{
			Methods:         []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:           "actors/{actorType}/{actorId}/timers/{name}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onCreateActorTimer,
		},
		{
			Methods:         []string{nethttp.MethodDelete},
			Route:           "actors/{actorType}/{actorId}/reminders/{name}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onDeleteActorReminder,
		},
		{
			Methods:         []string{nethttp.MethodDelete},
			Route:           "actors/{actorType}/{actorId}/timers/{name}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onDeleteActorTimer,
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "actors/{actorType}/{actorId}/reminders/{name}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onGetActorReminder,
		},
		{
			Methods:         []string{nethttp.MethodPatch},
			Route:           "actors/{actorType}/{actorId}/reminders/{name}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onRenameActorReminder,
		},
	}
}

func (a *api) constructHealthzEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "healthz",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onGetHealthz,
			AlwaysAllowed:   true,
			IsHealthCheck:   true,
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "healthz/outbound",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onGetOutboundHealthz,
			AlwaysAllowed:   true,
			IsHealthCheck:   true,
		},
	}
}

func (a *api) constructConfigurationEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "configuration/{storeName}",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onGetConfiguration,
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "configuration/{storeName}",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onGetConfiguration,
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "configuration/{storeName}/subscribe",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onSubscribeConfiguration,
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "configuration/{storeName}/subscribe",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onSubscribeConfiguration,
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "configuration/{storeName}/{configurationSubscribeID}/unsubscribe",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onUnsubscribeConfiguration,
		},
		{
			Methods:         []string{nethttp.MethodGet},
			Route:           "configuration/{storeName}/{configurationSubscribeID}/unsubscribe",
			Version:         apiVersionV1,
			FastHTTPHandler: a.onUnsubscribeConfiguration,
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
		fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, resp.Data))
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
		fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, b))
		return
	}

	var key string
	reqs := make([]state.GetRequest, len(req.Keys))
	for i, k := range req.Keys {
		key, err = stateLoader.GetModifiedStateKey(k, storeName, a.universal.AppID)
		if err != nil {
			msg := messages.ErrMalformedRequest.WithFormat(err)
			universalFastHTTPErrorResponder(reqCtx, msg)
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
	policyRunner := resiliency.NewRunner[[]state.BulkGetResponse](reqCtx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
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
	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, b))
}

func (a *api) getStateStoreWithRequestValidation(reqCtx *fasthttp.RequestCtx) (state.Store, string, error) {
	if a.universal.CompStore.StateStoresLen() == 0 {
		err := messages.ErrStateStoresNotConfigured
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return nil, "", err
	}

	storeName := a.getStateStoreName(reqCtx)

	state, ok := a.universal.CompStore.GetStateStore(storeName)
	if !ok {
		err := messages.ErrStateStoreNotFound.WithFormat(storeName)
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return nil, "", err
	}
	return state, storeName, nil
}

// Route:   "workflows/{workflowComponent}/{workflowName}/start?instanceID={instanceID}",
// Workflow Component: Component specified in yaml
// Workflow Name: Name of the workflow to run
// Instance ID: Identifier of the specific run
func (a *api) onStartWorkflowHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.StartWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.StartWorkflowRequest, *runtimev1pb.StartWorkflowResponse]{
			// We pass the input body manually rather than parsing it using protojson
			SkipInputBody: true,
			InModifierFastHTTP: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.StartWorkflowRequest, error) {
				in.WorkflowName = reqCtx.UserValue(workflowName).(string)
				in.WorkflowComponent = reqCtx.UserValue(workflowComponent).(string)

				// The instance ID is optional. If not specified, we generate a random one.
				instanceID := string(reqCtx.QueryArgs().Peek(instanceID))
				if instanceID == "" {
					if randomID, err := uuid.NewRandom(); err == nil {
						instanceID = randomID.String()
					} else {
						return nil, err
					}
				}
				in.InstanceId = instanceID

				// We accept the HTTP request body as the input to the workflow
				// without making any assumptions about its format.
				in.Input = reqCtx.PostBody()
				return in, nil
			},
			SuccessStatusCode: nethttp.StatusAccepted,
		})
}

// Route: POST "workflows/{workflowComponent}/{instanceID}"
func (a *api) onGetWorkflowHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.GetWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.GetWorkflowRequest, *runtimev1pb.GetWorkflowResponse]{
			InModifierFastHTTP: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowRequest, error) {
				in.WorkflowComponent = reqCtx.UserValue(workflowComponent).(string)
				in.InstanceId = reqCtx.UserValue(instanceID).(string)
				return in, nil
			},
		})
}

// Route: POST "workflows/{workflowComponent}/{instanceID}/terminate"
func (a *api) onTerminateWorkflowHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.TerminateWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.TerminateWorkflowRequest, *emptypb.Empty]{
			InModifierFastHTTP: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.TerminateWorkflowRequest) (*runtimev1pb.TerminateWorkflowRequest, error) {
				in.WorkflowComponent = reqCtx.UserValue(workflowComponent).(string)
				in.InstanceId = reqCtx.UserValue(instanceID).(string)
				return in, nil
			},
			SuccessStatusCode: nethttp.StatusAccepted,
		})
}

// Route: POST "workflows/{workflowComponent}/{instanceID}/events/{eventName}"
func (a *api) onRaiseEventWorkflowHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.RaiseEventWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.RaiseEventWorkflowRequest, *emptypb.Empty]{
			// We pass the input body manually rather than parsing it using protojson
			SkipInputBody: true,
			InModifierFastHTTP: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.RaiseEventWorkflowRequest) (*runtimev1pb.RaiseEventWorkflowRequest, error) {
				in.InstanceId = reqCtx.UserValue(instanceID).(string)
				in.WorkflowComponent = reqCtx.UserValue(workflowComponent).(string)
				in.EventName = reqCtx.UserValue(eventName).(string)

				// We accept the HTTP request body as the payload of the workflow event
				// without making any assumptions about its format.
				in.EventData = reqCtx.PostBody()
				return in, nil
			},
			SuccessStatusCode: nethttp.StatusAccepted,
		})
}

// ROUTE: POST "workflows/{workflowComponent}/{instanceID}/pause"
func (a *api) onPauseWorkflowHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.PauseWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.PauseWorkflowRequest, *emptypb.Empty]{
			InModifierFastHTTP: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.PauseWorkflowRequest) (*runtimev1pb.PauseWorkflowRequest, error) {
				in.WorkflowComponent = reqCtx.UserValue(workflowComponent).(string)
				in.InstanceId = reqCtx.UserValue(instanceID).(string)
				return in, nil
			},
			SuccessStatusCode: nethttp.StatusAccepted,
		})
}

// ROUTE: POST "workflows/{workflowComponent}/{instanceID}/resume"
func (a *api) onResumeWorkflowHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.ResumeWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.ResumeWorkflowRequest, *emptypb.Empty]{
			InModifierFastHTTP: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.ResumeWorkflowRequest) (*runtimev1pb.ResumeWorkflowRequest, error) {
				in.WorkflowComponent = reqCtx.UserValue(workflowComponent).(string)
				in.InstanceId = reqCtx.UserValue(instanceID).(string)
				return in, nil
			},
			SuccessStatusCode: nethttp.StatusAccepted,
		})
}

func (a *api) onPurgeWorkflowHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.PurgeWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.PurgeWorkflowRequest, *emptypb.Empty]{
			InModifierFastHTTP: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.PurgeWorkflowRequest) (*runtimev1pb.PurgeWorkflowRequest, error) {
				in.WorkflowComponent = reqCtx.UserValue(workflowComponent).(string)
				in.InstanceId = reqCtx.UserValue(instanceID).(string)
				return in, nil
			},
			SuccessStatusCode: nethttp.StatusAccepted,
		})
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
	k, err := stateLoader.GetModifiedStateKey(key, storeName, a.universal.AppID)
	if err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		universalFastHTTPErrorResponder(reqCtx, msg)
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
	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, resp.Data))
}

func (a *api) getConfigurationStoreWithRequestValidation(reqCtx *fasthttp.RequestCtx) (configuration.Store, string, error) {
	if a.universal.CompStore.ConfigurationsLen() == 0 {
		msg := NewErrorResponse("ERR_CONFIGURATION_STORE_NOT_CONFIGURED", messages.ErrConfigurationStoresNotConfigured)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}

	storeName := a.getStateStoreName(reqCtx)

	conf, ok := a.universal.CompStore.GetConfiguration(storeName)
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

		policyRunner := resiliency.NewRunner[struct{}](ctx, policyDef)
		_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
			rResp, rErr := h.appChannel.InvokeMethod(ctx, req, "")
			if rErr != nil {
				return struct{}{}, rErr
			}
			if rResp != nil {
				defer rResp.Close()
			}

			if rResp != nil && rResp.Status().Code != nethttp.StatusOK {
				return struct{}{}, fmt.Errorf("error sending configuration item to application, status %d", rResp.Status().Code)
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
	if a.appChannel == nil {
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
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	respBytes, _ := json.Marshal(&subscribeConfigurationResponse{
		ID: subscribeID,
	})
	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, respBytes))
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
		fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusInternalServerError, errRespBytes))
		log.Debug(msg)
		return
	}
	respBytes, _ := json.Marshal(&UnsubscribeConfigurationResponse{
		Ok: true,
	})
	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, respBytes))
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
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Configuration),
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

	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, respBytes))
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
	k, err := stateLoader.GetModifiedStateKey(key, storeName, a.universal.AppID)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
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
		// merge metadata from URL query parameters
		if reqs[i].Metadata == nil {
			reqs[i].Metadata = metadata
		} else {
			for k, v := range metadata {
				reqs[i].Metadata[k] = v
			}
		}

		reqs[i].Key, err = stateLoader.GetModifiedStateKey(r.Key, storeName, a.universal.AppID)
		if err != nil {
			msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
			fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
			log.Debug(err)
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
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
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

type invokeError struct {
	statusCode int
	msg        ErrorResponse
}

func (ie invokeError) Error() string {
	return fmt.Sprintf("invokeError (statusCode='%d') msg.errorCode='%s' msg.message='%s'", ie.statusCode, ie.msg.ErrorCode, ie.msg.Message)
}

func (a *api) isHTTPEndpoint(appID string) bool {
	endpoint, ok := a.universal.CompStore.GetHTTPEndpoint(appID)
	return ok && endpoint.Name == appID
}

// getBaseURL takes an app id and checks if the app id is an HTTP endpoint CRD.
// It returns the baseURL if found.
func (a *api) getBaseURL(targetAppID string) string {
	endpoint, ok := a.universal.CompStore.GetHTTPEndpoint(targetAppID)
	if ok && endpoint.Name == targetAppID {
		return endpoint.Spec.BaseURL
	}
	return ""
}

func (a *api) onDirectMessage(reqCtx *fasthttp.RequestCtx) {
	targetID, invokeMethodName := findTargetIDAndMethod(string(reqCtx.URI().PathOriginal()), reqCtx.Request.Header.Peek)
	if targetID == "" {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", messages.ErrDirectInvokeNoAppID)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusNotFound, msg))
		return
	}

	// Store target and method as user values so they can be picked up by the tracing library
	reqCtx.SetUserValue("id", targetID)
	reqCtx.SetUserValue("method", invokeMethodName)

	verb := strings.ToUpper(string(reqCtx.Method()))
	if a.directMessaging == nil {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", messages.ErrDirectInvokeNotReady)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		return
	}

	var policyDef *resiliency.PolicyDefinition
	switch {
	case strings.HasPrefix(targetID, "http://") || strings.HasPrefix(targetID, "https://"):
		policyDef = a.universal.Resiliency.EndpointPolicy(targetID, targetID+"/"+invokeMethodName)

	case a.isHTTPEndpoint(targetID):
		// http endpoint CRD resource is detected being used for service invocation
		baseURL := a.getBaseURL(targetID)
		policyDef = a.universal.Resiliency.EndpointPolicy(targetID, targetID+":"+baseURL)

	default:
		// regular service to service invocation
		policyDef = a.universal.Resiliency.EndpointPolicy(targetID, targetID+":"+invokeMethodName)
	}

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
		reqCtx, policyDef,
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
				statusCode: nethttp.StatusInternalServerError,
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
			if statusCode != nethttp.StatusOK {
				// Close the response to replace the body
				_ = rResp.Close()
				var body []byte
				body, rErr = invokev1.ProtobufToJSON(resStatus)
				rResp.WithRawDataBytes(body)
				resStatus.Code = statusCode
				if rErr != nil {
					return rResp, invokeError{
						statusCode: nethttp.StatusInternalServerError,
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
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, NewErrorResponse("ERR_DIRECT_INVOKE", err.Error())))
		return
	}

	if resp != nil {
		headers := resp.Headers()
		if len(headers) > 0 {
			invokev1.InternalMetadataToHTTPHeader(reqCtx, headers, reqCtx.Response.Header.Add)
		}
	}

	invokeErr := invokeError{}
	if errors.As(err, &invokeErr) {
		fasthttpRespond(reqCtx, fasthttpResponseWithError(invokeErr.statusCode, invokeErr.msg))
		if resp != nil {
			_ = resp.Close()
		}
		return
	}

	if resp == nil {
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, NewErrorResponse("ERR_DIRECT_INVOKE", fmt.Sprintf(messages.ErrDirectInvoke, targetID, "response object is nil"))))
		return
	}
	defer resp.Close()

	statusCode := int(resp.Status().Code)

	body, err := resp.RawDataFull()
	if err != nil {
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, NewErrorResponse("ERR_DIRECT_INVOKE", fmt.Sprintf(messages.ErrDirectInvoke, targetID, err))))
		return
	}

	reqCtx.Response.Header.SetContentType(resp.ContentType())
	fasthttpRespond(reqCtx, fasthttpResponseWith(statusCode, body))
}

// findTargetIDAndMethod finds ID of the target service and method from the following three places:
// 1. HTTP header 'dapr-app-id' (path is method)
// 2. Basic auth header: `http://dapr-app-id:<service-id>@localhost:3500/<method>`
// 3. URL parameter: `http://localhost:3500/v1.0/invoke/<app-id>/method/<method>`
func findTargetIDAndMethod(path string, peekHeader func(string) []byte) (targetID string, method string) {
	if appID := peekHeader(daprAppID); len(appID) != 0 {
		return string(appID), strings.TrimPrefix(path, "/")
	}

	if auth := string(peekHeader(fasthttp.HeaderAuthorization)); strings.HasPrefix(auth, "Basic ") {
		if s, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Basic ")); err == nil {
			pair := strings.Split(string(s), ":")
			if len(pair) == 2 && pair[0] == daprAppID {
				return pair[1], strings.TrimPrefix(path, "/")
			}
		}
	}

	// If we're here, the handler was probably invoked with /v1.0/invoke/ (or the invocation is invalid, missing the app id provided as header or Basic auth)
	// However, we are not relying on wildcardParam because the URL may have been sanitized to remove `//``, so `http://` would have been turned into `http:/`
	// First, check to make sure that the path has the prefix
	if idx := pathHasPrefix(path, apiVersionV1, "invoke"); idx > 0 {
		path = path[idx:]

		// Scan to find app ID and method
		// Matches `<appid>/method/<method>`.
		// Examples:
		// - `appid/method/mymethod`
		// - `http://example.com/method/mymethod`
		// - `https://example.com/method/mymethod`
		// - `http%3A%2F%2Fexample.com/method/mymethod`
		if idx = strings.Index(path, "/method/"); idx > 0 {
			targetID = path[:idx]
			method = path[(idx + len("/method/")):]
			if t, _ := url.QueryUnescape(targetID); t != "" {
				targetID = t
			}
			return
		}
	}

	return "", ""
}

// Returns true if a path has the parts as prefix (and a trailing slash), and returns the index of the first byte after the prefix (and after any trailing slashes).
func pathHasPrefix(path string, prefixParts ...string) int {
	pl := len(path)
	ppl := len(prefixParts)
	if pl == 0 {
		return -1
	}

	var i, start, found int
	for i = 0; i < pl; i++ {
		if path[i] != '/' {
			if found >= ppl {
				return i
			}
			continue
		}

		if i-start > 0 {
			if path[start:i] == prefixParts[found] {
				found++
			} else {
				return -1
			}
		}
		start = i + 1
	}
	if found >= ppl {
		return i
	}
	return -1
}

func (a *api) onCreateActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.universal.Actors == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
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

	err = a.universal.Actors.CreateReminder(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_CREATE", fmt.Sprintf(messages.ErrActorReminderCreate, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
}

func (a *api) onRenameActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.universal.Actors == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	var req actors.RenameReminderRequest
	err := json.Unmarshal(reqCtx.PostBody(), &req)
	if err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		universalFastHTTPErrorResponder(reqCtx, msg)
		log.Debug(msg)
		return
	}

	req.OldName = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.universal.Actors.RenameReminder(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_RENAME", fmt.Sprintf(messages.ErrActorReminderRename, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
}

func (a *api) onCreateActorTimer(reqCtx *fasthttp.RequestCtx) {
	if a.universal.Actors == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
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

	err = a.universal.Actors.CreateTimer(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_TIMER_CREATE", fmt.Sprintf(messages.ErrActorTimerCreate, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
}

func (a *api) onDeleteActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.universal.Actors == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
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

	err := a.universal.Actors.DeleteReminder(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_DELETE", fmt.Sprintf(messages.ErrActorReminderDelete, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
}

func (a *api) onActorStateTransaction(reqCtx *fasthttp.RequestCtx) {
	if a.universal.Actors == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
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
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	hosted := a.universal.Actors.IsActorHosted(reqCtx, &actors.ActorHostedRequest{
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

	err = a.universal.Actors.TransactionalStateOperation(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_TRANSACTION_SAVE", fmt.Sprintf(messages.ErrActorStateTransactionSave, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
}

func (a *api) onGetActorReminder(reqCtx *fasthttp.RequestCtx) {
	if a.universal.Actors == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	name := reqCtx.UserValue(nameParam).(string)

	resp, err := a.universal.Actors.GetReminder(reqCtx, &actors.GetReminderRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Name:      name,
	})
	if err != nil {
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

	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, b))
}

func (a *api) onDeleteActorTimer(reqCtx *fasthttp.RequestCtx) {
	if a.universal.Actors == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
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
	err := a.universal.Actors.DeleteTimer(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_TIMER_DELETE", fmt.Sprintf(messages.ErrActorTimerDelete, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
}

func (a *api) onDirectActorMessage(reqCtx *fasthttp.RequestCtx) {
	if a.universal.Actors == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	verb := strings.ToUpper(string(reqCtx.Method()))
	method := reqCtx.UserValue(methodParam).(string)

	policyDef := a.resiliency.ActorPreLockPolicy(actorType, actorID)

	req := invokev1.NewInvokeMethodRequest(method).
		WithActor(actorType, actorID).
		WithHTTPExtension(verb, reqCtx.QueryArgs().String()).
		WithRawDataBytes(reqCtx.PostBody()).
		WithContentType(string(reqCtx.Request.Header.ContentType())).
		// Save headers to internal metadata
		WithFastHTTPHeaders(&reqCtx.Request.Header)
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
		return a.universal.Actors.Call(ctx, req)
	})
	if err != nil && !errors.Is(err, actors.ErrDaprResponseHeader) {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if resp == nil {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, "failed to cast response"))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	defer resp.Close()

	// Use Add to ensure headers are appended and not replaced
	invokev1.InternalMetadataToHTTPHeader(reqCtx, resp.Headers(), reqCtx.Response.Header.Add)
	body, err := resp.RawDataFull()
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	reqCtx.Response.Header.SetContentType(resp.ContentType())

	// Construct response.
	statusCode := int(resp.Status().Code)
	if !resp.IsHTTPResponse() {
		statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
	}
	fasthttpRespond(reqCtx, fasthttpResponseWith(statusCode, body))
}

func (a *api) onGetActorState(reqCtx *fasthttp.RequestCtx) {
	if a.universal.Actors == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	actorType := reqCtx.UserValue(actorTypeParam).(string)
	actorID := reqCtx.UserValue(actorIDParam).(string)
	key := reqCtx.UserValue(stateKeyParam).(string)

	hosted := a.universal.Actors.IsActorHosted(reqCtx, &actors.ActorHostedRequest{
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

	resp, err := a.universal.Actors.GetState(reqCtx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_GET", fmt.Sprintf(messages.ErrActorStateGet, err))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		if resp == nil || len(resp.Data) == 0 {
			fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
			return
		}
		fasthttpRespond(reqCtx, fasthttpResponseWithJSON(nethttp.StatusOK, resp.Data))
	}
}

func (a *api) onPublish(reqCtx *fasthttp.RequestCtx) {
	thepubsub, pubsubName, topic, sc, errRes := a.validateAndGetPubsubAndTopic(reqCtx)
	if errRes != nil {
		fasthttpRespond(reqCtx, fasthttpResponseWithError(sc, *errRes))

		return
	}

	body := reqCtx.PostBody()
	contentType := string(reqCtx.Request.Header.Peek("Content-Type"))
	metadata := getMetadataFromFastHTTPRequest(reqCtx)
	rawPayload, metaErr := contribMetadata.IsRawPayload(metadata)
	if metaErr != nil {
		msg := messages.ErrPubSubMetadataDeserialize.WithFormat(metaErr)
		universalFastHTTPErrorResponder(reqCtx, msg)
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
			Source:          a.universal.AppID,
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
			fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
			log.Debug(msg)
			return
		}

		features := thepubsub.Features()

		pubsub.ApplyMetadata(envelope, features, metadata)

		data, err = json.Marshal(envelope)
		if err != nil {
			msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER",
				fmt.Sprintf(messages.ErrPubsubCloudEventsSer, topic, pubsubName, err.Error()))
			fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
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
		status := nethttp.StatusInternalServerError
		msg := NewErrorResponse("ERR_PUBSUB_PUBLISH_MESSAGE",
			fmt.Sprintf(messages.ErrPubsubPublishMessage, topic, pubsubName, err.Error()))

		if errors.As(err, &runtimePubsub.NotAllowedError{}) {
			msg = NewErrorResponse("ERR_PUBSUB_FORBIDDEN", err.Error())
			status = nethttp.StatusForbidden
		}

		if errors.As(err, &runtimePubsub.NotFoundError{}) {
			msg = NewErrorResponse("ERR_PUBSUB_NOT_FOUND", err.Error())
			status = nethttp.StatusBadRequest
		}

		fasthttpRespond(reqCtx, fasthttpResponseWithError(status, msg))
		log.Debug(msg)
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
	thepubsub, pubsubName, topic, sc, errRes := a.validateAndGetPubsubAndTopic(reqCtx)
	if errRes != nil {
		fasthttpRespond(reqCtx, fasthttpResponseWithError(sc, errRes))

		return
	}

	body := reqCtx.PostBody()
	metadata := getMetadataFromFastHTTPRequest(reqCtx)
	rawPayload, metaErr := contribMetadata.IsRawPayload(metadata)
	if metaErr != nil {
		msg := messages.ErrPubSubMetadataDeserialize.WithFormat(metaErr)
		universalFastHTTPErrorResponder(reqCtx, msg)
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
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
		log.Debug(msg)

		return
	}
	entries := make([]pubsub.BulkMessageEntry, len(incomingEntries))

	entryIDSet := map[string]struct{}{}

	for i, entry := range incomingEntries {
		var dBytes []byte
		dBytes, err = ConvertEventToBytes(entry.Event, entry.ContentType)
		if err != nil {
			msg := NewErrorResponse("ERR_PUBSUB_EVENTS_SER",
				fmt.Sprintf(messages.ErrPubsubMarshal, topic, pubsubName, err.Error()))
			fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
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
		if _, ok := entryIDSet[entry.EntryID]; ok || entry.EntryID == "" {
			msg := NewErrorResponse("ERR_PUBSUB_EVENTS_SER",
				fmt.Sprintf(messages.ErrPubsubMarshal, topic, pubsubName, "error: entryId is duplicated or not present for entry"))
			fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
			log.Debug(msg)

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
			// For multiple events in a single bulk call traceParent is different for each event.
			childSpan := diag.StartProducerSpanChildFromParent(reqCtx, span)
			// Populate W3C traceparent to cloudevent envelope
			corID := diag.SpanContextToW3CString(childSpan.SpanContext())
			spanMap[i] = childSpan

			var envelope map[string]interface{}
			envelope, err = runtimePubsub.NewCloudEvent(&runtimePubsub.CloudEvent{
				Source:          a.universal.AppID,
				Topic:           topic,
				DataContentType: entries[i].ContentType,
				Data:            entries[i].Event,
				TraceID:         corID,
				TraceState:      traceState,
				Pubsub:          pubsubName,
			}, entries[i].Metadata)
			if err != nil {
				msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER",
					fmt.Sprintf(messages.ErrPubsubCloudEventCreation, err.Error()))
				fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg), closeChildSpans)
				log.Debug(msg)

				return
			}

			pubsub.ApplyMetadata(envelope, features, entries[i].Metadata)

			entries[i].Event, err = json.Marshal(envelope)
			if err != nil {
				msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER",
					fmt.Sprintf(messages.ErrPubsubCloudEventsSer, topic, pubsubName, err.Error()))
				fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg), closeChildSpans)
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
		status := nethttp.StatusInternalServerError
		bulkRes.ErrorCode = "ERR_PUBSUB_PUBLISH_MESSAGE"

		if errors.As(err, &runtimePubsub.NotAllowedError{}) {
			msg := NewErrorResponse("ERR_PUBSUB_FORBIDDEN", err.Error())
			status = nethttp.StatusForbidden
			fasthttpRespond(reqCtx, fasthttpResponseWithError(status, msg), closeChildSpans)
			log.Debug(msg)

			return
		}

		if errors.As(err, &runtimePubsub.NotFoundError{}) {
			msg := NewErrorResponse("ERR_PUBSUB_NOT_FOUND", err.Error())
			status = nethttp.StatusBadRequest
			fasthttpRespond(reqCtx, fasthttpResponseWithError(status, msg), closeChildSpans)
			log.Debug(msg)

			return
		}

		// Return the error along with the list of failed entries.
		resData, _ := json.Marshal(bulkRes)
		fasthttpRespond(reqCtx, fasthttpResponseWithJSON(status, resData), closeChildSpans)
		return
	}

	// If there are no errors, then an empty response is returned.
	fasthttpRespond(reqCtx, fasthttpResponseWithEmpty(), closeChildSpans)
}

// validateAndGetPubsubAndTopic takes input as request context and returns the pubsub interface, pubsub name, topic name,
// or error status code and an ErrorResponse object.
func (a *api) validateAndGetPubsubAndTopic(reqCtx *fasthttp.RequestCtx) (pubsub.PubSub, string, string, int, *ErrorResponse) {
	if a.pubsubAdapter == nil {
		msg := NewErrorResponse("ERR_PUBSUB_NOT_CONFIGURED", messages.ErrPubsubNotConfigured)

		return nil, "", "", nethttp.StatusBadRequest, &msg
	}

	pubsubName := reqCtx.UserValue(pubsubnameparam).(string)
	if pubsubName == "" {
		msg := NewErrorResponse("ERR_PUBSUB_EMPTY", messages.ErrPubsubEmpty)

		return nil, "", "", nethttp.StatusNotFound, &msg
	}

	thepubsub := a.pubsubAdapter.GetPubSub(pubsubName)
	if thepubsub == nil {
		msg := NewErrorResponse("ERR_PUBSUB_NOT_FOUND", fmt.Sprintf(messages.ErrPubsubNotFound, pubsubName))

		return nil, "", "", nethttp.StatusNotFound, &msg
	}

	topic := reqCtx.UserValue(wildcardParam).(string)
	if topic == "" {
		msg := NewErrorResponse("ERR_TOPIC_EMPTY", fmt.Sprintf(messages.ErrTopicEmpty, pubsubName))

		return nil, "", "", nethttp.StatusNotFound, &msg
	}
	return thepubsub, pubsubName, topic, nethttp.StatusOK, nil
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

func (a *api) onGetHealthz(reqCtx *fasthttp.RequestCtx) {
	if !a.readyStatus {
		msg := NewErrorResponse("ERR_HEALTH_NOT_READY", messages.ErrHealthNotReady)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
}

func (a *api) onGetOutboundHealthz(reqCtx *fasthttp.RequestCtx) {
	if !a.outboundReadyStatus {
		msg := NewErrorResponse("ERR_OUTBOUND_HEALTH_NOT_READY", messages.ErrOutboundHealthNotReady)
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
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
	if a.universal.CompStore.StateStoresLen() == 0 {
		err := messages.ErrStateStoresNotConfigured
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}

	storeName := reqCtx.UserValue(storeNameParam).(string)
	store, ok := a.universal.CompStore.GetStateStore(storeName)
	if !ok {
		err := messages.ErrStateStoreNotFound.WithFormat(storeName)
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}

	transactionalStore, ok := store.(state.TransactionalStore)
	if !ok {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_SUPPORTED", fmt.Sprintf(messages.ErrStateStoreNotSupported, storeName))
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
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

	operations := make([]state.TransactionalStateOperation, len(req.Operations))
	for i, o := range req.Operations {
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
			upsertReq.Key, err = stateLoader.GetModifiedStateKey(upsertReq.Key, storeName, a.universal.AppID)
			if err != nil {
				msg := messages.ErrMalformedRequest.WithFormat(err)
				universalFastHTTPErrorResponder(reqCtx, msg)
				log.Debug(err)
				return
			}
			operations[i] = upsertReq
		case string(state.OperationDelete):
			var delReq state.DeleteRequest
			err := mapstructure.Decode(o.Request, &delReq)
			if err != nil {
				msg := messages.ErrMalformedRequest.WithFormat(err)
				universalFastHTTPErrorResponder(reqCtx, msg)
				log.Debug(msg)
				return
			}
			delReq.Key, err = stateLoader.GetModifiedStateKey(delReq.Key, storeName, a.universal.AppID)
			if err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
				fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
				log.Debug(msg)
				return
			}
			operations[i] = delReq
		default:
			msg := NewErrorResponse(
				"ERR_NOT_SUPPORTED_STATE_OPERATION",
				fmt.Sprintf(messages.ErrNotSupportedStateOperation, o.Operation))
			fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusBadRequest, msg))
			log.Debug(msg)
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
		fasthttpRespond(reqCtx, fasthttpResponseWithError(nethttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
	}
}

func (a *api) onQueryStateHandler() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.QueryStateAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.QueryStateRequest, *runtimev1pb.QueryStateResponse]{
			// We pass the input body manually rather than parsing it using protojson
			SkipInputBody: true,
			InModifierFastHTTP: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.QueryStateRequest) (*runtimev1pb.QueryStateRequest, error) {
				in.StoreName = reqCtx.UserValue(storeNameParam).(string)
				in.Metadata = getMetadataFromFastHTTPRequest(reqCtx)
				in.Query = string(reqCtx.PostBody())
				return in, nil
			},
			OutModifier: func(out *runtimev1pb.QueryStateResponse) (any, error) {
				// If the response is empty, return nil
				if out == nil || len(out.Results) == 0 {
					return nil, nil
				}

				// We need to translate this to a JSON object because one of the fields must be returned as json.RawMessage
				qresp := &QueryResponse{
					Results:  make([]QueryItem, len(out.Results)),
					Token:    out.Token,
					Metadata: out.Metadata,
				}
				for i := range out.Results {
					qresp.Results[i].Key = stateLoader.GetOriginalStateKey(out.Results[i].Key)
					if out.Results[i].Etag != "" {
						qresp.Results[i].ETag = &out.Results[i].Etag
					}
					qresp.Results[i].Error = out.Results[i].Error
					qresp.Results[i].Data = json.RawMessage(out.Results[i].Data)
				}
				return qresp, nil
			},
		},
	)
}

func (a *api) SetAppChannel(appChannel channel.AppChannel) {
	a.appChannel = appChannel
}

func (a *api) SetHTTPEndpointsAppChannel(appChannel channel.HTTPEndpointAppChannel) {
	a.httpEndpointsAppChannel = appChannel
}

func (a *api) SetDirectMessaging(directMessaging messaging.DirectMessaging) {
	a.directMessaging = directMessaging
}

func (a *api) SetActorRuntime(actor actors.Actors) {
	a.universal.Actors = actor
}
