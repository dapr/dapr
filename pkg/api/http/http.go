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
	"go.opentelemetry.io/otel/trace"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/state"
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
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messages/errorcodes"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/outbox"
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
}

type api struct {
	universal             *universal.Universal
	endpoints             []endpoints.Endpoint
	publicEndpoints       []endpoints.Endpoint
	directMessaging       invokev1.DirectMessaging
	channels              *channels.Channels
	pubsubAdapter         runtimePubsub.Adapter
	outbox                outbox.Outbox
	sendToOutputBindingFn func(ctx context.Context, name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	metricSpec            *config.MetricSpec
	tracingSpec           config.TracingSpec
	maxRequestBodySize    int64 // In bytes
	healthz               healthz.Healthz
	outboundHealthz       healthz.Healthz
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
	PubSubAdapter         runtimePubsub.Adapter
	Outbox                outbox.Outbox
	SendToOutputBindingFn func(ctx context.Context, name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	TracingSpec           config.TracingSpec
	MetricSpec            *config.MetricSpec
	MaxRequestBodySize    int64 // In bytes
	Healthz               healthz.Healthz
	OutboundHealthz       healthz.Healthz
}

// NewAPI returns a new API.
func NewAPI(opts APIOpts) API {
	api := &api{
		universal:             opts.Universal,
		channels:              opts.Channels,
		directMessaging:       opts.DirectMessaging,
		pubsubAdapter:         opts.PubSubAdapter,
		outbox:                opts.Outbox,
		sendToOutputBindingFn: opts.SendToOutputBindingFn,
		tracingSpec:           opts.TracingSpec,
		metricSpec:            opts.MetricSpec,
		maxRequestBodySize:    opts.MaxRequestBodySize,
		healthz:               opts.Healthz,
		outboundHealthz:       opts.OutboundHealthz,
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
	api.endpoints = append(api.endpoints, api.constructJobsEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructConversationEndpoints()...)

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
			Methods: []string{nethttp.MethodGet},
			Route:   "state/{storeName}/{key}",
			Version: apiVersionV1,
			Group:   endpointGroupStateV1,
			Handler: a.onGetState,
			Settings: endpoints.EndpointSettings{
				Name: "GetState",
			},
		},
		{
			Methods: []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:   "state/{storeName}",
			Version: apiVersionV1,
			Group:   endpointGroupStateV1,
			Handler: a.onPostState,
			Settings: endpoints.EndpointSettings{
				Name: "SaveState",
			},
		},
		{
			Methods: []string{nethttp.MethodDelete},
			Route:   "state/{storeName}/{key}",
			Version: apiVersionV1,
			Group:   endpointGroupStateV1,
			Handler: a.onDeleteState,
			Settings: endpoints.EndpointSettings{
				Name: "DeleteState",
			},
		},
		{
			Methods: []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:   "state/{storeName}/bulk",
			Version: apiVersionV1,
			Group:   endpointGroupStateV1,
			Handler: a.onBulkGetState,
			Settings: endpoints.EndpointSettings{
				Name: "GetBulkState",
			},
		},
		{
			Methods: []string{nethttp.MethodPost, nethttp.MethodPut},
			Route:   "state/{storeName}/transaction",
			Version: apiVersionV1,
			Group:   endpointGroupStateV1,
			Handler: a.onPostStateTransaction,
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
			Handler: a.onPublish,
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
			Handler: a.onBulkPublish,
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
			Handler: a.onOutputBindingMessage,
			Settings: endpoints.EndpointSettings{
				Name: "InvokeBinding",
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
			Methods: []string{nethttp.MethodGet},
			Route:   "configuration/{storeName}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupConfigurationV1Alpha1,
			Handler: a.onGetConfiguration,
			Settings: endpoints.EndpointSettings{
				Name: "GetConfiguration",
			},
		},
		{
			Methods: []string{nethttp.MethodGet},
			Route:   "configuration/{storeName}",
			Version: apiVersionV1,
			Group:   endpointGroupConfigurationV1,
			Handler: a.onGetConfiguration,
			Settings: endpoints.EndpointSettings{
				Name: "GetConfiguration",
			},
		},
		{
			Methods: []string{nethttp.MethodGet},
			Route:   "configuration/{storeName}/subscribe",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupConfigurationV1Alpha1,
			Handler: a.onSubscribeConfiguration,
			Settings: endpoints.EndpointSettings{
				Name: "SubscribeConfiguration",
			},
		},
		{
			Methods: []string{nethttp.MethodGet},
			Route:   "configuration/{storeName}/subscribe",
			Version: apiVersionV1,
			Group:   endpointGroupConfigurationV1,
			Handler: a.onSubscribeConfiguration,
			Settings: endpoints.EndpointSettings{
				Name: "SubscribeConfiguration",
			},
		},
		{
			Methods: []string{nethttp.MethodGet},
			Route:   "configuration/{storeName}/{configurationSubscribeID}/unsubscribe",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupConfigurationV1Alpha1,
			Handler: a.onUnsubscribeConfiguration,
			Settings: endpoints.EndpointSettings{
				Name: "UnsubscribeConfiguration",
			},
		},
		{
			Methods: []string{nethttp.MethodGet},
			Route:   "configuration/{storeName}/{configurationSubscribeID}/unsubscribe",
			Version: apiVersionV1,
			Group:   endpointGroupConfigurationV1,
			Handler: a.onUnsubscribeConfiguration,
			Settings: endpoints.EndpointSettings{
				Name: "UnsubscribeConfiguration",
			},
		},
	}
}

func (a *api) onOutputBindingMessage(w nethttp.ResponseWriter, r *nethttp.Request) {
	name := chi.URLParam(r, nameParam)

	var req OutputBindingRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	b, err := json.Marshal(req.Data)
	if err != nil {
		resp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrMalformedRequestData, err), errorcodes.CommonMalformedRequestData, nethttp.StatusInternalServerError)
		respondWithError(w, resp)
		log.Debug(resp)
		return
	}

	// pass the trace context to output binding in metadata
	if span := diagUtils.SpanFromContext(r.Context()); span != nil {
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
	resp, err := a.sendToOutputBindingFn(r.Context(), name, &bindings.InvokeRequest{
		Metadata:  req.Metadata,
		Data:      b,
		Operation: bindings.OperationKind(req.Operation),
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.OutputBindingEvent(context.Background(), name, req.Operation, err == nil, elapsed)

	if resp != nil {
		// Set the metadata in the response even in case of error.
		// HTTP binding, for example, returns metadata even for error.
		for k, v := range resp.Metadata {
			w.Header().Add(metadataPrefix+k, v)
		}
	}

	if err != nil {
		resp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrInvokeOutputBinding, name, err), errorcodes.BindingInvokeOutputBinding, nethttp.StatusInternalServerError)
		respondWithError(w, resp)
		log.Debug(resp)
		return
	}

	if resp == nil {
		respondWithEmpty(w)
	} else {
		respondWithData(w, nethttp.StatusOK, resp.Data)
	}
}

func (a *api) onBulkGetState(w nethttp.ResponseWriter, r *nethttp.Request) {
	store, storeName, err := a.getStateStoreWithRequestValidation(w, r)
	if err != nil {
		log.Debug(err)
		return
	}

	var req BulkGetRequest
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}

	// merge metadata from URL query parameters
	metadata := getMetadataFromRequest(r)
	if req.Metadata == nil {
		req.Metadata = metadata
	} else {
		for k, v := range metadata {
			req.Metadata[k] = v
		}
	}

	bulkResp := make([]BulkGetResponse, len(req.Keys))
	if len(req.Keys) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(bulkResp)
		return
	}

	var key string
	reqs := make([]state.GetRequest, len(req.Keys))
	for i, k := range req.Keys {
		key, err = stateLoader.GetModifiedStateKey(k, storeName, a.universal.AppID())
		if err != nil {
			status := apierrors.StateStore(storeName).InvalidKeyName(k, err.Error())
			respondWithError(w, status)
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
	policyRunner := resiliency.NewRunner[[]state.BulkGetResponse](r.Context(),
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
		code := nethttp.StatusInternalServerError
		kerr, ok := kiterrors.FromError(err)
		if ok {
			code = kerr.HTTPStatusCode()
		}

		resp := messages.NewAPIErrorHTTP(err.Error(), errorcodes.StateBulkGet, code)
		respondWithError(w, resp)
		log.Debug(resp)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(bulkResp)
}

func (a *api) getStateStoreWithRequestValidation(w nethttp.ResponseWriter, r *nethttp.Request) (state.Store, string, error) {
	storeName := chi.URLParam(r, storeNameParam)

	if a.universal.CompStore().StateStoresLen() == 0 {
		err := apierrors.StateStore(storeName).NotConfigured(a.universal.AppID())
		log.Debug(err)
		respondWithError(w, err)
		return nil, "", err
	}

	stateStore, ok := a.universal.CompStore().GetStateStore(storeName)
	if !ok {
		err := apierrors.StateStore(storeName).NotFound(a.universal.AppID())
		log.Debug(err)
		respondWithError(w, err)
		return nil, "", err
	}
	return stateStore, storeName, nil
}

func (a *api) onGetState(w nethttp.ResponseWriter, r *nethttp.Request) {
	store, storeName, err := a.getStateStoreWithRequestValidation(w, r)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromRequest(r)

	key := chi.URLParam(r, stateKeyParam)
	consistency := r.URL.Query().Get(consistencyParam)
	k, err := stateLoader.GetModifiedStateKey(key, storeName, a.universal.AppID())
	if err != nil {
		status := apierrors.StateStore(storeName).InvalidKeyName(key, err.Error())
		respondWithError(w, status)
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
	policyRunner := resiliency.NewRunner[*state.GetResponse](r.Context(),
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	resp, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
		return store.Get(ctx, req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(context.Background(), storeName, diag.Get, err == nil, elapsed)

	if err != nil {
		code := nethttp.StatusInternalServerError
		kerr, ok := kiterrors.FromError(err)
		if ok {
			code = kerr.HTTPStatusCode()
		}

		resp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrStateGet, key, storeName, err.Error()), errorcodes.StateGet, code)
		respondWithError(w, resp)
		log.Debug(resp)
		return
	}

	if resp == nil || resp.Data == nil {
		respondWithEmpty(w)
		return
	}

	if encryption.EncryptedStateStore(storeName) {
		val, err := encryption.TryDecryptValue(storeName, resp.Data)
		if err != nil {
			resp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrStateGet, key, storeName, err.Error()), errorcodes.StateGet, nethttp.StatusInternalServerError)
			respondWithError(w, resp)
			log.Debug(resp)
			return
		}

		resp.Data = val
	}

	if resp.ETag != nil {
		w.Header().Add(etagHeader, *resp.ETag)
	}

	setResponseMetadataHeaders(w, resp.Metadata)

	respondWithData(w, nethttp.StatusOK, resp.Data)
}

func (a *api) getConfigurationStoreWithRequestValidation(w nethttp.ResponseWriter, r *nethttp.Request) (configuration.Store, string, error) {
	if a.universal.CompStore().ConfigurationsLen() == 0 {
		resp := messages.NewAPIErrorHTTP(messages.ErrConfigurationStoresNotConfigured, errorcodes.ConfigurationStoreNotConfigured, nethttp.StatusInternalServerError)
		respondWithError(w, resp)
		log.Debug(resp)
		return nil, "", errors.New(resp.Message())
	}

	storeName := chi.URLParam(r, storeNameParam)

	conf, ok := a.universal.CompStore().GetConfiguration(storeName)
	if !ok {
		resp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrConfigurationStoreNotFound, storeName), errorcodes.ConfigurationStoreNotFound, nethttp.StatusBadRequest)
		respondWithError(w, resp)
		log.Debug(resp)
		return nil, "", errors.New(resp.Message())
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

func (a *api) onSubscribeConfiguration(w nethttp.ResponseWriter, r *nethttp.Request) {
	store, storeName, err := a.getConfigurationStoreWithRequestValidation(w, r)
	if err != nil {
		log.Debug(err)
		return
	}
	if a.channels.AppChannel() == nil {
		msg := NewErrorResponse(errorcodes.CommonAppChannelNil, "app channel is not initialized. cannot subscribe to configuration updates")
		respondWithJSON(w, nethttp.StatusInternalServerError, msg)
		log.Debug(msg)
		return
	}
	metadata := getMetadataFromRequest(r)
	subscribeKeys := make([]string, 0)

	keys := make([]string, 0)
	for _, queryKey := range r.URL.Query()[configurationKeyParam] {
		keys = append(keys, queryKey)
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
	// TODO: @joshvanl: This TODO context should be based on the server context, and
	// closed on Dapr shutdown.
	policyRunner := resiliency.NewRunner[string](context.TODO(),
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Configuration),
	)
	subscribeID, err := policyRunner(func(ctx context.Context) (string, error) {
		return store.Subscribe(ctx, req, handler.updateEventHandler)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), storeName, diag.ConfigurationSubscribe, err == nil, elapsed)

	if err != nil {
		resp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrConfigurationSubscribe, keys, storeName, err.Error()), errorcodes.ConfigurationSubscribe, nethttp.StatusInternalServerError)
		respondWithError(w, resp)
		log.Debug(resp)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(&subscribeConfigurationResponse{
		ID: subscribeID,
	})
}

func (a *api) onUnsubscribeConfiguration(w nethttp.ResponseWriter, r *nethttp.Request) {
	store, storeName, err := a.getConfigurationStoreWithRequestValidation(w, r)
	if err != nil {
		log.Debug(err)
		return
	}
	subscribeID := chi.URLParam(r, configurationSubscribeID)

	req := configuration.UnsubscribeRequest{
		ID: subscribeID,
	}
	start := time.Now()
	policyRunner := resiliency.NewRunner[any](r.Context(),
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Configuration),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.Unsubscribe(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)
	diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), storeName, diag.ConfigurationUnsubscribe, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse(errorcodes.ConfigurationUnsubscribe, fmt.Sprintf(messages.ErrConfigurationUnsubscribe, subscribeID, err.Error()))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(nethttp.StatusInternalServerError)
		json.NewEncoder(w).Encode(&UnsubscribeConfigurationResponse{
			Ok:      false,
			Message: msg.Message,
		})
		log.Debug(msg)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(&UnsubscribeConfigurationResponse{
		Ok: true,
	})
}

func (a *api) onGetConfiguration(w nethttp.ResponseWriter, r *nethttp.Request) {
	store, storeName, err := a.getConfigurationStoreWithRequestValidation(w, r)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromRequest(r)

	keys := make([]string, 0)
	for _, key := range r.URL.Query()[configurationKeyParam] {
		keys = append(keys, key)
	}
	req := &configuration.GetRequest{
		Keys:     keys,
		Metadata: metadata,
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*configuration.GetResponse](r.Context(),
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Configuration),
	)
	getResponse, err := policyRunner(func(ctx context.Context) (*configuration.GetResponse, error) {
		return store.Get(ctx, req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), storeName, diag.Get, err == nil, elapsed)

	if err != nil {
		resp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrConfigurationGet, keys, storeName, err.Error()), errorcodes.ConfigurationGet, nethttp.StatusInternalServerError)
		respondWithError(w, resp)
		log.Debug(resp)
		return
	}

	if getResponse == nil || getResponse.Items == nil || len(getResponse.Items) == 0 {
		respondWithEmpty(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(getResponse.Items)
}

func extractEtag(r *nethttp.Request) (hasEtag bool, etag string) {
	_, ok := r.Header["If-Match"]
	return ok, r.Header.Get("If-Match")
}

func (a *api) onDeleteState(w nethttp.ResponseWriter, r *nethttp.Request) {
	store, storeName, err := a.getStateStoreWithRequestValidation(w, r)
	if err != nil {
		log.Debug(err)
		return
	}

	key := chi.URLParam(r, stateKeyParam)

	concurrency := r.URL.Query().Get(concurrencyParam)
	consistency := r.URL.Query().Get(consistencyParam)

	metadata := getMetadataFromRequest(r)
	k, err := stateLoader.GetModifiedStateKey(key, storeName, a.universal.AppID())
	if err != nil {
		status := apierrors.StateStore(storeName).InvalidKeyName(key, err.Error())
		respondWithError(w, status)
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

	exists, etag := extractEtag(r)
	if exists {
		req.ETag = &etag
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[any](r.Context(),
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.Delete(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(r.Context(), storeName, diag.Delete, err == nil, elapsed)

	if err != nil {
		statusCode, errMsg := a.stateErrorResponse(err)
		apiResp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrStateDelete, key, errMsg), errorcodes.StateDelete, statusCode)
		respondWithError(w, apiResp)
		log.Debug(apiResp)
		return
	}
	respondWithEmpty(w)
}

func (a *api) onPostState(w nethttp.ResponseWriter, r *nethttp.Request) {
	store, storeName, err := a.getStateStoreWithRequestValidation(w, r)
	if err != nil {
		log.Debug(err)
		return
	}

	if err != nil {
		resp := messages.NewAPIErrorHTTP(err.Error(), errorcodes.CommonMalformedRequest, nethttp.StatusBadRequest)
		respondWithError(w, resp)
		log.Debug(resp)
		return
	}
	reqs := []state.SetRequest{}
	err = json.NewDecoder(r.Body).Decode(&reqs)
	if err != nil {
		resp := messages.NewAPIErrorHTTP(err.Error(), errorcodes.CommonMalformedRequest, nethttp.StatusBadRequest)
		respondWithError(w, resp)
		log.Debug(resp)
		return
	}
	if len(reqs) == 0 {
		respondWithEmpty(w)
		return
	}

	metadata := getMetadataFromRequest(r)

	for i, r := range reqs {
		if len(reqs[i].Key) == 0 {
			resp := messages.NewAPIErrorHTTP(`"key" is a required field`, errorcodes.CommonMalformedRequest, nethttp.StatusBadRequest)
			respondWithError(w, resp)
			log.Debug(resp)
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
			status := apierrors.StateStore(storeName).InvalidKeyName(r.Key, err.Error())
			respondWithError(w, status)
			log.Debug(status)
			return
		}

		if encryption.EncryptedStateStore(storeName) {
			data := []byte(fmt.Sprintf("%v", r.Value))
			val, encErr := encryption.TryEncryptValue(storeName, data)
			if encErr != nil {
				statusCode, errMsg := a.stateErrorResponse(encErr)
				apiResp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrStateSave, storeName, errMsg), errorcodes.StateSave, statusCode)
				respondWithError(w, apiResp)
				log.Debug(apiResp)
				return
			}

			reqs[i].Value = val
		}
	}

	start := time.Now()
	err = stateLoader.PerformBulkStoreOperation(r.Context(), reqs,
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Statestore),
		state.BulkStoreOpts{},
		store.Set,
		store.BulkSet,
	)
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(r.Context(), storeName, diag.Set, err == nil, elapsed)

	if err != nil {
		statusCode, errMsg := a.stateErrorResponse(err)
		apiResp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrStateSave, storeName, errMsg), errorcodes.StateSave, statusCode)
		respondWithError(w, apiResp)
		log.Debug(apiResp)
		return
	}

	respondWithEmpty(w)
}

// stateErrorResponse takes a state store error and returns a corresponding status code, error message and modified user error.
func (a *api) stateErrorResponse(err error) (int, string) {
	etag, code, message := a.etagError(err)

	if etag {
		return code, message
	}
	message = err.Error()

	standardizedErr, ok := kiterrors.FromError(err)
	if ok {
		return standardizedErr.HTTPStatusCode(), standardizedErr.Error()
	}

	return nethttp.StatusInternalServerError, message
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

func (a *api) onPublish(w nethttp.ResponseWriter, r *nethttp.Request) {
	thepubsub, pubsubName, topic, validationErr := a.validateAndGetPubsubAndTopic(r)

	if validationErr != nil {
		log.Debug(validationErr)
		respondWithError(w, validationErr)
		return
	}

	body, readErr := io.ReadAll(r.Body)
	defer r.Body.Close()
	if readErr != nil {
		err := apierrors.PubSub(pubsubName).PublishMessage(topic, readErr)
		respondWithError(w, err)
		log.Debug(err)
		return
	}

	contentType := r.Header.Get("Content-Type")
	metadata := getMetadataFromRequest(r)
	rawPayload, metaErr := contribMetadata.IsRawPayload(metadata)
	if metaErr != nil {
		err := apierrors.PubSub(pubsubName).WithMetadata(metadata).DeserializeError(metaErr)
		respondWithError(w, err)
		log.Debug(err)
		return
	}

	data := body

	if !rawPayload {
		span := diagUtils.SpanFromContext(r.Context())
		traceID, traceState := diag.TraceIDAndStateFromSpan(span)
		envelope, err := runtimePubsub.NewCloudEvent(&runtimePubsub.CloudEvent{
			Source:          a.universal.AppID(),
			Topic:           topic,
			DataContentType: contentType,
			Data:            body,
			TraceID:         traceID,
			TraceState:      traceState,
			Pubsub:          pubsubName,
		}, metadata)
		if err != nil {
			nerr := apierrors.PubSub(pubsubName).WithAppError(
				a.universal.AppID(), err,
			).CloudEventCreation()
			respondWithError(w, nerr)
			log.Debug(nerr)
			return
		}

		features := thepubsub.Features()

		pubsub.ApplyMetadata(envelope, features, metadata)

		data, err = json.Marshal(envelope)
		if err != nil {
			nerr := apierrors.PubSub(pubsubName).WithAppError(
				a.universal.AppID(), err,
			).WithTopic(topic).MarshalEnvelope()
			respondWithError(w, nerr)
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
	err := a.pubsubAdapter.Publish(r.Context(), &req)
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.PubsubEgressEvent(context.Background(), pubsubName, topic, err == nil, elapsed)

	if err != nil {
		var nerr error

		switch {
		case errors.As(err, &runtimePubsub.NotAllowedError{}):
			nerr = apierrors.PubSub(pubsubName).PublishForbidden(topic, a.universal.AppID(), err)
		case errors.As(err, &runtimePubsub.NotFoundError{}):
			nerr = apierrors.PubSub(pubsubName).TestNotFound(topic, err)
		default:
			nerr = apierrors.PubSub(pubsubName).PublishMessage(topic, err)
		}

		respondWithError(w, nerr)
		log.Debug(nerr)
	} else {
		respondWithEmpty(w)
	}
}

type bulkPublishMessageEntry struct {
	EntryID     string            `json:"entryId,omitempty"`
	Event       interface{}       `json:"event"`
	ContentType string            `json:"contentType"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func (a *api) onBulkPublish(w nethttp.ResponseWriter, r *nethttp.Request) {
	thepubsub, pubsubName, topic, validationErr := a.validateAndGetPubsubAndTopic(r)

	if validationErr != nil {
		log.Debug(validationErr)
		respondWithError(w, validationErr)
		return
	}

	metadata := getMetadataFromRequest(r)
	rawPayload, metaErr := contribMetadata.IsRawPayload(metadata)
	if metaErr != nil {
		err := apierrors.PubSub(pubsubName).WithMetadata(metadata).DeserializeError(metaErr)
		log.Debug(err)
		respondWithError(w, err)
		return
	}

	// Extract trace context from context.
	span := diagUtils.SpanFromContext(r.Context())

	incomingEntries := make([]bulkPublishMessageEntry, 0)
	err := json.NewDecoder(r.Body).Decode(&incomingEntries)
	if err != nil {
		nerr := apierrors.PubSub(pubsubName).WithAppError(
			a.universal.AppID(), nil,
		).WithTopic(topic).UnmarshalEvents(err)
		respondWithError(w, nerr)
		log.Debug(nerr)
		return
	}
	entries := make([]pubsub.BulkMessageEntry, len(incomingEntries))

	entryIDSet := map[string]struct{}{}

	for i, entry := range incomingEntries {
		var dBytes []byte
		dBytes, err = ConvertEventToBytes(entry.Event, entry.ContentType)
		if err != nil {
			nerr := apierrors.PubSub(pubsubName).WithAppError(
				a.universal.AppID(), err,
			).WithTopic(topic).MarshalEnvelope()
			respondWithError(w, nerr)
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
			nerr := apierrors.PubSub(pubsubName).WithAppError(
				a.universal.AppID(),
				errors.New("entryId is duplicated or not present for entry"),
			).WithTopic(topic).MarshalEvents()
			respondWithError(w, nerr)
			log.Debug(nerr)
			return
		}
		entryIDSet[entry.EntryID] = struct{}{}
		entries[i].EntryId = entry.EntryID
	}

	spanMap := map[int]trace.Span{}
	// closeChildSpans method is called on every respond() call in all return paths in the following block of code.
	closeChildSpans := func(statusCode int) {
		for _, span := range spanMap {
			diag.UpdateSpanStatusFromHTTPStatus(span, statusCode)
			span.End()
		}
	}
	features := thepubsub.Features()
	if !rawPayload {
		for i := range entries {
			childSpan := diag.StartProducerSpanChildFromParent(r, span)
			traceID, traceState := diag.TraceIDAndStateFromSpan(childSpan)
			// For multiple events in a single bulk call traceParent is different for each event.
			// Populate W3C traceparent to cloudevent envelope
			spanMap[i] = childSpan

			var envelope map[string]interface{}
			envelope, err = runtimePubsub.NewCloudEvent(&runtimePubsub.CloudEvent{
				Source:          a.universal.AppID(),
				Topic:           topic,
				DataContentType: entries[i].ContentType,
				Data:            entries[i].Event,
				TraceID:         traceID,
				TraceState:      traceState,
				Pubsub:          pubsubName,
			}, entries[i].Metadata)
			if err != nil {
				nerr := apierrors.PubSub(pubsubName).WithAppError(
					a.universal.AppID(), err,
				).CloudEventCreation()
				standardizedErr, ok := kiterrors.FromError(nerr)
				if ok {
					closeChildSpans(standardizedErr.HTTPStatusCode())
					respondWithError(w, standardizedErr)
				}
				log.Debug(nerr)
				return
			}

			pubsub.ApplyMetadata(envelope, features, entries[i].Metadata)

			entries[i].Event, err = json.Marshal(envelope)
			if err != nil {
				nerr := apierrors.PubSub(pubsubName).WithAppError(
					a.universal.AppID(), nil,
				).WithTopic(topic).MarshalEnvelope()
				standardizedErr, ok := kiterrors.FromError(nerr)
				if ok {
					closeChildSpans(standardizedErr.HTTPStatusCode())
					respondWithError(w, standardizedErr)
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
	res, err := a.pubsubAdapter.BulkPublish(r.Context(), &req)
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

		switch {
		case errors.As(err, &runtimePubsub.NotAllowedError{}):
			nerr := apierrors.PubSub(pubsubName).PublishForbidden(topic, a.universal.AppID(), err)
			standardizedErr, ok := kiterrors.FromError(nerr)
			if ok {
				closeChildSpans(standardizedErr.HTTPStatusCode())
				respondWithError(w, standardizedErr)
			}
			log.Debug(nerr)
			return
		case errors.As(err, &runtimePubsub.NotFoundError{}):
			nerr := apierrors.PubSub(pubsubName).TestNotFound(topic, err)
			standardizedErr, ok := kiterrors.FromError(nerr)
			if ok {
				closeChildSpans(standardizedErr.HTTPStatusCode())
				respondWithError(w, standardizedErr)
			}
			return
		default:
			err = apierrors.PubSub(pubsubName).PublishMessage(topic, err)
			log.Debug(err)
		}

		// Return the error along with the list of failed entries.
		bulkRes.ErrorCode = errorcodes.PubsubPublishMessage.Code
		resData, _ := json.Marshal(bulkRes)
		if standardizedErr, ok := kiterrors.FromError(err); ok {
			setResponseMetadataHeaders(w, map[string]string{"responseData": string(resData), "error": standardizedErr.Error()})
			closeChildSpans(standardizedErr.HTTPStatusCode())
			respondWithDataAndRecordError(w, standardizedErr.HTTPStatusCode(), resData, &errorcodes.PubsubPublishMessage)
		}
		return
	}

	// If there are no errors, then an empty response is returned.
	closeChildSpans(nethttp.StatusOK)
	respondWithEmpty(w)
}

// validateAndGetPubsubAndTopic takes input as request context and returns the pubsub interface, pubsub name, topic name,
// or error status code and an ErrorResponse object.
func (a *api) validateAndGetPubsubAndTopic(r *nethttp.Request) (pubsub.PubSub, string, string, error) {
	var err error

	pubsubName := chi.URLParam(r, pubsubnameparam)
	metadata := getMetadataFromRequest(r)

	if a.pubsubAdapter == nil {
		err = apierrors.PubSub(pubsubName).WithMetadata(metadata).NotConfigured()
		return nil, "", "", err
	}

	if pubsubName == "" {
		err = apierrors.PubSub(pubsubName).WithMetadata(metadata).NameEmpty()
		return nil, "", "", err
	}

	thepubsub, ok := a.universal.CompStore().GetPubSub(pubsubName)
	if !ok {
		err = apierrors.PubSub(pubsubName).WithMetadata(metadata).NotFound()
		return nil, "", "", err
	}

	topic := chi.URLParam(r, wildcardParam)
	if topic == "" {
		err = apierrors.PubSub(pubsubName).WithMetadata(metadata).TopicEmpty()
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

type stateTransactionRequestBody struct {
	Operations []stateTransactionRequestBodyOperation `json:"operations"`
	Metadata   map[string]string                      `json:"metadata,omitempty"`
}

type stateTransactionRequestBodyOperation struct {
	Operation string      `json:"operation"`
	Request   interface{} `json:"request"`
}

func (a *api) onPostStateTransaction(w nethttp.ResponseWriter, r *nethttp.Request) {
	storeName := chi.URLParam(r, storeNameParam)

	if a.universal.CompStore().StateStoresLen() == 0 {
		err := apierrors.StateStore(storeName).NotConfigured(a.universal.AppID())
		log.Debug(err)
		respondWithError(w, err)
		return
	}

	store, ok := a.universal.CompStore().GetStateStore(storeName)
	if !ok {
		err := apierrors.StateStore(storeName).NotFound(a.universal.AppID())
		log.Debug(err)
		respondWithError(w, err)
		return
	}

	transactionalStore, ok := store.(state.TransactionalStore)
	if !ok || !state.FeatureTransactional.IsPresent(store.Features()) {
		err := apierrors.StateStore(storeName).TransactionsNotSupported()
		respondWithError(w, err)
		log.Debug(err)
		return
	}

	var req stateTransactionRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		msg := messages.ErrMalformedRequest.WithFormat(err)
		respondWithError(w, msg)
		log.Debug(msg)
		return
	}
	if len(req.Operations) == 0 {
		respondWithEmpty(w)
		return
	}

	// merge metadata from URL query parameters
	metadata := getMetadataFromRequest(r)
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
				respondWithError(w, msg)
				log.Debug(msg)
				return
			}
			upsertReq.Key, err = stateLoader.GetModifiedStateKey(upsertReq.Key, storeName, a.universal.AppID())
			if err != nil {
				status := apierrors.StateStore(storeName).InvalidKeyName(upsertReq.Key, err.Error())
				respondWithError(w, status)
				log.Debug(status)
				return
			}

			if req.Metadata != nil {
				if upsertReq.Metadata == nil {
					upsertReq.Metadata = metadata
				} else {
					for k, v := range metadata {
						upsertReq.Metadata[k] = v
					}
				}
			}

			operations = append(operations, upsertReq)
		case string(state.OperationDelete):
			var delReq state.DeleteRequest
			err := mapstructure.Decode(o.Request, &delReq)
			if err != nil {
				msg := messages.ErrMalformedRequest.WithFormat(err)
				respondWithError(w, msg)
				log.Debug(msg)
				return
			}
			delReq.Key, err = stateLoader.GetModifiedStateKey(delReq.Key, storeName, a.universal.AppID())
			if err != nil {
				status := apierrors.StateStore(storeName).InvalidKeyName(delReq.Key, err.Error())
				respondWithError(w, status)
				log.Debug(status)

				return
			}

			if req.Metadata != nil {
				if delReq.Metadata == nil {
					delReq.Metadata = metadata
				} else {
					for k, v := range metadata {
						delReq.Metadata[k] = v
					}
				}
			}

			operations = append(operations, delReq)
		default:
			resp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrNotSupportedStateOperation, o.Operation), errorcodes.StateNotSupportedOperation, nethttp.StatusBadRequest)
			respondWithError(w, resp)
			log.Debug(resp)
			return
		}
	}

	if maxMulti, ok := store.(state.TransactionalStoreMultiMaxSize); ok {
		max := maxMulti.MultiMaxSize()
		if max > 0 && len(operations) > max {
			err := apierrors.StateStore(storeName).TooManyTransactionalOps(len(operations), max)
			log.Debug(err)
			respondWithError(w, err)
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
					resp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrStateSave, storeName, err.Error()), errorcodes.StateSave, nethttp.StatusBadRequest)
					respondWithError(w, resp)
					log.Debug(resp)
					return
				}

				req.Value = val
				operations[i] = req
			}
		}
	}

	outboxEnabled := a.outbox.Enabled(storeName)
	if outboxEnabled {
		span := diagUtils.SpanFromContext(r.Context())
		corID, traceState := diag.TraceIDAndStateFromSpan(span)
		ops, err := a.outbox.PublishInternal(r.Context(), storeName, operations, a.universal.AppID(), corID, traceState)
		if err != nil {
			nerr := apierrors.PubSubOutbox(a.universal.AppID(), err)
			respondWithError(w, nerr)
			log.Debug(nerr)
			return
		}

		operations = ops
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[any](r.Context(),
		a.universal.Resiliency().ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	storeReq := &state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   req.Metadata,
	}
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, transactionalStore.Multi(r.Context(), storeReq)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(context.Background(), storeName, diag.StateTransaction, err == nil, elapsed)

	if err != nil {
		resp := messages.NewAPIErrorHTTP(fmt.Sprintf(messages.ErrStateTransaction, err.Error()), errorcodes.StateTransaction, nethttp.StatusInternalServerError)
		respondWithError(w, resp)
		log.Debug(resp)
	} else {
		respondWithEmpty(w)
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
				defer r.Body.Close()
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
