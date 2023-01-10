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

package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	otelTrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/components-contrib/contenttype"
	"github.com/dapr/components-contrib/health"
	"github.com/dapr/components-contrib/lock"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/actors"
	componentsV1alpha "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/channel"
	lockLoader "github.com/dapr/dapr/pkg/components/lock"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/grpc/metadata"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/utils"
)

const (
	daprHTTPStatusHeader  = "dapr-http-status"
	daprRuntimeVersionKey = "daprRuntimeVersion"
)

// API is the gRPC interface for the Dapr gRPC API. It implements both the internal and external proto definitions.
type API interface {
	// DaprInternal Service methods
	internalv1pb.ServiceInvocationServer

	// Dapr Service methods
	PublishEvent(ctx context.Context, in *runtimev1pb.PublishEventRequest) (*emptypb.Empty, error)
	BulkPublishEventAlpha1(ctx context.Context, req *runtimev1pb.BulkPublishRequest) (*runtimev1pb.BulkPublishResponse, error)
	InvokeService(ctx context.Context, in *runtimev1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error)
	InvokeBinding(ctx context.Context, in *runtimev1pb.InvokeBindingRequest) (*runtimev1pb.InvokeBindingResponse, error)
	GetComponentHealthAlpha1(ctx context.Context, in *runtimev1pb.ComponentHealthRequest) (*runtimev1pb.ComponentHealthResponse, error)
	GetState(ctx context.Context, in *runtimev1pb.GetStateRequest) (*runtimev1pb.GetStateResponse, error)
	GetBulkState(ctx context.Context, in *runtimev1pb.GetBulkStateRequest) (*runtimev1pb.GetBulkStateResponse, error)
	GetSecret(ctx context.Context, in *runtimev1pb.GetSecretRequest) (*runtimev1pb.GetSecretResponse, error)
	GetBulkSecret(ctx context.Context, in *runtimev1pb.GetBulkSecretRequest) (*runtimev1pb.GetBulkSecretResponse, error)
	GetConfigurationAlpha1(ctx context.Context, in *runtimev1pb.GetConfigurationRequest) (*runtimev1pb.GetConfigurationResponse, error)
	SubscribeConfigurationAlpha1(request *runtimev1pb.SubscribeConfigurationRequest, configurationServer runtimev1pb.Dapr_SubscribeConfigurationAlpha1Server) error
	UnsubscribeConfigurationAlpha1(ctx context.Context, request *runtimev1pb.UnsubscribeConfigurationRequest) (*runtimev1pb.UnsubscribeConfigurationResponse, error)
	SaveState(ctx context.Context, in *runtimev1pb.SaveStateRequest) (*emptypb.Empty, error)
	QueryStateAlpha1(ctx context.Context, in *runtimev1pb.QueryStateRequest) (*runtimev1pb.QueryStateResponse, error)
	DeleteState(ctx context.Context, in *runtimev1pb.DeleteStateRequest) (*emptypb.Empty, error)
	DeleteBulkState(ctx context.Context, in *runtimev1pb.DeleteBulkStateRequest) (*emptypb.Empty, error)
	ExecuteStateTransaction(ctx context.Context, in *runtimev1pb.ExecuteStateTransactionRequest) (*emptypb.Empty, error)
	runtimev1pb.DaprServer

	// Methods internal to the object
	SetAppChannel(appChannel channel.AppChannel)
	SetDirectMessaging(directMessaging messaging.DirectMessaging)
	SetActorRuntime(actor actors.Actors)
}

type api struct {
	actor                      actors.Actors
	directMessaging            messaging.DirectMessaging
	appChannel                 channel.AppChannel
	resiliency                 resiliency.Provider
	stateStores                map[string]state.Store
	workflowComponents         map[string]workflows.Workflow
	transactionalStateStores   map[string]state.TransactionalStore
	secretStores               map[string]secretstores.SecretStore
	inputBindings              map[string]bindings.InputBinding
	outputBindings             map[string]bindings.OutputBinding
	secretsConfiguration       map[string]config.SecretsScope
	configurationStores        map[string]configuration.Store
	configurationSubscribe     map[string]chan struct{} // store map[storeName||key1,key2] -> stopChan
	configurationSubscribeLock sync.Mutex
	lockStores                 map[string]lock.Store
	pubsubAdapter              runtimePubsub.Adapter
	id                         string
	sendToOutputBindingFn      func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	tracingSpec                config.TracingSpec
	accessControlList          *config.AccessControlList
	appProtocol                string
	extendedMetadata           sync.Map
	shutdown                   func()
	getComponentsFn            func() []componentsV1alpha.Component
	getComponentsCapabilitesFn func() map[string][]string
	getSubscriptionsFn         func() ([]runtimePubsub.Subscription, error)
	daprRunTimeVersion         string
}

func (a *api) TryLockAlpha1(ctx context.Context, req *runtimev1pb.TryLockRequest) (*runtimev1pb.TryLockResponse, error) {
	// 1. validate
	if a.lockStores == nil || len(a.lockStores) == 0 {
		err := status.Error(codes.FailedPrecondition, messages.ErrLockStoresNotConfigured)
		apiServerLogger.Debug(err)
		return &runtimev1pb.TryLockResponse{}, err
	}
	if req.ResourceId == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrResourceIDEmpty, req.StoreName)
		return &runtimev1pb.TryLockResponse{}, err
	}
	if req.LockOwner == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrLockOwnerEmpty, req.StoreName)
		return &runtimev1pb.TryLockResponse{}, err
	}
	if req.ExpiryInSeconds <= 0 {
		err := status.Errorf(codes.InvalidArgument, messages.ErrExpiryInSecondsNotPositive, req.StoreName)
		return &runtimev1pb.TryLockResponse{}, err
	}
	// 2. find lock component
	store, ok := a.lockStores[req.StoreName]
	if !ok {
		return &runtimev1pb.TryLockResponse{}, status.Errorf(codes.InvalidArgument, messages.ErrLockStoreNotFound, req.StoreName)
	}
	// 3. convert request
	compReq := TryLockRequestToComponentRequest(req)
	// modify key
	var err error
	compReq.ResourceID, err = lockLoader.GetModifiedLockKey(compReq.ResourceID, req.StoreName, a.id)
	if err != nil {
		apiServerLogger.Debug(err)
		return &runtimev1pb.TryLockResponse{}, err
	}
	// 4. delegate to the component
	compResp, err := store.TryLock(ctx, compReq)
	if err != nil {
		apiServerLogger.Debug(err)
		return &runtimev1pb.TryLockResponse{}, err
	}
	// 5. convert response
	resp := TryLockResponseToGrpcResponse(compResp)
	return resp, nil
}

func (a *api) UnlockAlpha1(ctx context.Context, req *runtimev1pb.UnlockRequest) (*runtimev1pb.UnlockResponse, error) {
	// 1. validate
	if a.lockStores == nil || len(a.lockStores) == 0 {
		err := status.Error(codes.FailedPrecondition, messages.ErrLockStoresNotConfigured)
		apiServerLogger.Debug(err)
		return newInternalErrorUnlockResponse(), err
	}
	if req.ResourceId == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrResourceIDEmpty, req.StoreName)
		return newInternalErrorUnlockResponse(), err
	}
	if req.LockOwner == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrLockOwnerEmpty, req.StoreName)
		return newInternalErrorUnlockResponse(), err
	}
	// 2. find store component
	store, ok := a.lockStores[req.StoreName]
	if !ok {
		return newInternalErrorUnlockResponse(), status.Errorf(codes.InvalidArgument, messages.ErrLockStoreNotFound, req.StoreName)
	}
	// 3. convert request
	compReq := UnlockGrpcToComponentRequest(req)
	// modify key
	var err error
	compReq.ResourceID, err = lockLoader.GetModifiedLockKey(compReq.ResourceID, req.StoreName, a.id)
	if err != nil {
		apiServerLogger.Debug(err)
		return newInternalErrorUnlockResponse(), err
	}
	// 4. delegate to the component
	compResp, err := store.Unlock(ctx, compReq)
	if err != nil {
		apiServerLogger.Debug(err)
		return newInternalErrorUnlockResponse(), err
	}
	// 5. convert response
	resp := UnlockResponseToGrpcResponse(compResp)
	return resp, nil
}

func newInternalErrorUnlockResponse() *runtimev1pb.UnlockResponse {
	return &runtimev1pb.UnlockResponse{
		Status: runtimev1pb.UnlockResponse_INTERNAL_ERROR, //nolint:nosnakecase
	}
}

func TryLockRequestToComponentRequest(req *runtimev1pb.TryLockRequest) *lock.TryLockRequest {
	result := &lock.TryLockRequest{}
	if req == nil {
		return result
	}
	result.ResourceID = req.ResourceId
	result.LockOwner = req.LockOwner
	result.ExpiryInSeconds = req.ExpiryInSeconds
	return result
}

func TryLockResponseToGrpcResponse(compResponse *lock.TryLockResponse) *runtimev1pb.TryLockResponse {
	result := &runtimev1pb.TryLockResponse{}
	if compResponse == nil {
		return result
	}
	result.Success = compResponse.Success
	return result
}

func UnlockGrpcToComponentRequest(req *runtimev1pb.UnlockRequest) *lock.UnlockRequest {
	result := &lock.UnlockRequest{}
	if req == nil {
		return result
	}
	result.ResourceID = req.ResourceId
	result.LockOwner = req.LockOwner
	return result
}

func UnlockResponseToGrpcResponse(compResp *lock.UnlockResponse) *runtimev1pb.UnlockResponse {
	result := &runtimev1pb.UnlockResponse{}
	if compResp == nil {
		return result
	}
	result.Status = runtimev1pb.UnlockResponse_Status(compResp.Status) //nolint:nosnakecase
	return result
}

// APIOpts contains options for NewAPI.
type APIOpts struct {
	AppID                       string
	AppChannel                  channel.AppChannel
	Resiliency                  resiliency.Provider
	StateStores                 map[string]state.Store
	SecretStores                map[string]secretstores.SecretStore
	SecretsConfiguration        map[string]config.SecretsScope
	ConfigurationStores         map[string]configuration.Store
	WorkflowComponents          map[string]workflows.Workflow
	LockStores                  map[string]lock.Store
	PubsubAdapter               runtimePubsub.Adapter
	DirectMessaging             messaging.DirectMessaging
	Actor                       actors.Actors
	SendToOutputBindingFn       func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	TracingSpec                 config.TracingSpec
	AccessControlList           *config.AccessControlList
	AppProtocol                 string
	Shutdown                    func()
	GetComponentsFn             func() []componentsV1alpha.Component
	GetComponentsCapabilitiesFn func() map[string][]string
	GetSubscriptionsFn          func() ([]runtimePubsub.Subscription, error)
	InputBindings               map[string]bindings.InputBinding
	OutputBindings              map[string]bindings.OutputBinding
}

// NewAPI returns a new gRPC API.
func NewAPI(opts APIOpts) API {
	transactionalStateStores := map[string]state.TransactionalStore{}
	for key, store := range opts.StateStores {
		if state.FeatureTransactional.IsPresent(store.Features()) {
			transactionalStateStores[key] = store.(state.TransactionalStore)
		}
	}
	return &api{
		directMessaging:            opts.DirectMessaging,
		actor:                      opts.Actor,
		id:                         opts.AppID,
		resiliency:                 opts.Resiliency,
		appChannel:                 opts.AppChannel,
		pubsubAdapter:              opts.PubsubAdapter,
		stateStores:                opts.StateStores,
		inputBindings:              opts.InputBindings,
		outputBindings:             opts.OutputBindings,
		transactionalStateStores:   transactionalStateStores,
		secretStores:               opts.SecretStores,
		workflowComponents:         opts.WorkflowComponents,
		configurationStores:        opts.ConfigurationStores,
		configurationSubscribe:     make(map[string]chan struct{}),
		lockStores:                 opts.LockStores,
		secretsConfiguration:       opts.SecretsConfiguration,
		sendToOutputBindingFn:      opts.SendToOutputBindingFn,
		tracingSpec:                opts.TracingSpec,
		accessControlList:          opts.AccessControlList,
		appProtocol:                opts.AppProtocol,
		shutdown:                   opts.Shutdown,
		getComponentsFn:            opts.GetComponentsFn,
		getComponentsCapabilitesFn: opts.GetComponentsCapabilitiesFn,
		getSubscriptionsFn:         opts.GetSubscriptionsFn,
		daprRunTimeVersion:         buildinfo.Version(),
	}
}

// validateAndGetPubsbuAndTopic validates the request parameters and returns the pubsub interface, pubsub name, topic name, rawPayload metadata if set
// or an error.
func (a *api) validateAndGetPubsubAndTopic(pubsubName, topic string, reqMeta map[string]string) (pubsub.PubSub, string, string, bool, error) {
	if a.pubsubAdapter == nil {
		return nil, "", "", false, status.Error(codes.FailedPrecondition, messages.ErrPubsubNotConfigured)
	}

	if pubsubName == "" {
		return nil, "", "", false, status.Error(codes.InvalidArgument, messages.ErrPubsubEmpty)
	}

	thepubsub := a.pubsubAdapter.GetPubSub(pubsubName)
	if thepubsub == nil {
		return nil, "", "", false, status.Errorf(codes.InvalidArgument, messages.ErrPubsubNotFound, pubsubName)
	}

	if topic == "" {
		return nil, "", "", false, status.Errorf(codes.InvalidArgument, messages.ErrTopicEmpty, pubsubName)
	}

	rawPayload, metaErr := contribMetadata.IsRawPayload(reqMeta)
	if metaErr != nil {
		return nil, "", "", false, status.Errorf(codes.InvalidArgument, messages.ErrMetadataGet, metaErr.Error())
	}

	return thepubsub, pubsubName, topic, rawPayload, nil
}

func (a *api) PublishEvent(ctx context.Context, in *runtimev1pb.PublishEventRequest) (*emptypb.Empty, error) {
	thepubsub, pubsubName, topic, rawPayload, validationErr := a.validateAndGetPubsubAndTopic(in.PubsubName, in.Topic, in.Metadata)
	if validationErr != nil {
		apiServerLogger.Debug(validationErr)
		return &emptypb.Empty{}, validationErr
	}

	span := diagUtils.SpanFromContext(ctx)
	// Populate W3C traceparent to cloudevent envelope
	corID := diag.SpanContextToW3CString(span.SpanContext())
	// Populate W3C tracestate to cloudevent envelope
	traceState := diag.TraceStateToW3CString(span.SpanContext())

	body := []byte{}
	if in.Data != nil {
		body = in.Data
	}

	data := body

	if !rawPayload {
		envelope, err := runtimePubsub.NewCloudEvent(&runtimePubsub.CloudEvent{
			ID:              a.id,
			Topic:           in.Topic,
			DataContentType: in.DataContentType,
			Data:            body,
			TraceID:         corID,
			TraceState:      traceState,
			Pubsub:          in.PubsubName,
		})
		if err != nil {
			err = status.Errorf(codes.InvalidArgument, messages.ErrPubsubCloudEventCreation, err.Error())
			apiServerLogger.Debug(err)
			return &emptypb.Empty{}, err
		}

		features := thepubsub.Features()
		pubsub.ApplyMetadata(envelope, features, in.Metadata)

		data, err = json.Marshal(envelope)
		if err != nil {
			err = status.Errorf(codes.InvalidArgument, messages.ErrPubsubCloudEventsSer, topic, pubsubName, err.Error())
			apiServerLogger.Debug(err)
			return &emptypb.Empty{}, err
		}
	}

	req := pubsub.PublishRequest{
		PubsubName: pubsubName,
		Topic:      topic,
		Data:       data,
		Metadata:   in.Metadata,
	}

	start := time.Now()
	err := a.pubsubAdapter.Publish(&req)
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.PubsubEgressEvent(context.Background(), pubsubName, topic, err == nil, elapsed)

	if err != nil {
		nerr := status.Errorf(codes.Internal, messages.ErrPubsubPublishMessage, topic, pubsubName, err.Error())
		if errors.As(err, &runtimePubsub.NotAllowedError{}) {
			nerr = status.Errorf(codes.PermissionDenied, err.Error())
		}

		if errors.As(err, &runtimePubsub.NotFoundError{}) {
			nerr = status.Errorf(codes.NotFound, err.Error())
		}
		apiServerLogger.Debug(nerr)
		return &emptypb.Empty{}, nerr
	}

	return &emptypb.Empty{}, nil
}

type invokeServiceResp struct {
	message  *commonv1pb.InvokeResponse
	headers  metadata.MD
	trailers metadata.MD
}

// Deprecated: Use proxy mode service invocation instead.
func (a *api) InvokeService(ctx context.Context, in *runtimev1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error) {
	if a.directMessaging == nil {
		return nil, status.Errorf(codes.Internal, messages.ErrDirectInvokeNotReady)
	}

	apiServerLogger.Warn("[DEPRECATION NOTICE] InvokeService is deprecated and will be removed in the future, please use proxy mode instead.")
	policyDef := a.resiliency.EndpointPolicy(in.Id, in.Id+":"+in.Message.Method)

	req := invokev1.FromInvokeRequestMessage(in.GetMessage()).
		WithReplay(policyDef.HasRetries())
	defer req.Close()

	if incomingMD, ok := metadata.FromIncomingContext(ctx); ok {
		req.WithMetadata(incomingMD)
	}

	policyRunner := resiliency.NewRunner[*invokeServiceResp](ctx, policyDef)
	resp, err := policyRunner(func(ctx context.Context) (*invokeServiceResp, error) {
		rResp := &invokeServiceResp{}
		imr, rErr := a.directMessaging.Invoke(ctx, in.Id, req)
		if imr != nil {
			// Read the entire message in memory then close imr
			pd, pdErr := imr.ProtoWithData()
			imr.Close()
			if pd != nil {
				rResp.message = pd.Message
			}

			// If we have an error, set it only if rErr is not already set
			if pdErr != nil && rErr == nil {
				rErr = pdErr
			}
		}
		if rErr != nil {
			return rResp, status.Errorf(codes.Internal, messages.ErrDirectInvoke, in.Id, rErr)
		}

		rResp.headers = invokev1.InternalMetadataToGrpcMetadata(ctx, imr.Headers(), true)

		if imr.IsHTTPResponse() {
			apiServerLogger.Warn("[DEPRECATION NOTICE] Invocation path of gRPC -> HTTP is deprecated and will be removed in the future.")
			var errorMessage string
			if rResp.message != nil && rResp.message.Data != nil {
				errorMessage = string(rResp.message.Data.Value)
			}
			code := int(imr.Status().Code)
			// If the status is OK, will be nil
			rErr = invokev1.ErrorFromHTTPResponseCode(code, errorMessage)
			// Populate http status code to header
			rResp.headers.Set(daprHTTPStatusHeader, strconv.Itoa(code))
		} else {
			// If the status is OK, will be nil
			rErr = invokev1.ErrorFromInternalStatus(imr.Status())
			// Only include trailers if appchannel uses gRPC
			rResp.trailers = invokev1.InternalMetadataToGrpcMetadata(ctx, imr.Trailers(), false)
		}

		return rResp, rErr
	})

	var message *commonv1pb.InvokeResponse
	if resp != nil {
		if resp.headers != nil {
			grpc.SetHeader(ctx, resp.headers)
		}
		if resp.trailers != nil {
			grpc.SetTrailer(ctx, resp.trailers)
		}
		message = resp.message
	}

	// In this case, there was an error with the actual request or a resiliency policy stopped the request.
	if err != nil {
		// Check if it's returned by status.Errorf
		_, ok := err.(interface{ GRPCStatus() *status.Status })
		if ok || errors.Is(err, context.DeadlineExceeded) || breaker.IsErrorPermanent(err) {
			return nil, err
		}
	}

	return message, err
}

func (a *api) BulkPublishEventAlpha1(ctx context.Context, in *runtimev1pb.BulkPublishRequest) (*runtimev1pb.BulkPublishResponse, error) {
	thepubsub, pubsubName, topic, rawPayload, validationErr := a.validateAndGetPubsubAndTopic(in.PubsubName, in.Topic, in.Metadata)
	if validationErr != nil {
		apiServerLogger.Debug(validationErr)
		return &runtimev1pb.BulkPublishResponse{}, validationErr
	}

	span := diagUtils.SpanFromContext(ctx)
	// Populate W3C tracestate to cloudevent envelope
	traceState := diag.TraceStateToW3CString(span.SpanContext())

	spanMap := map[int]otelTrace.Span{}
	// closeChildSpans method is called on every respond() call in all return paths in the following block of code.
	closeChildSpans := func(ctx context.Context, err error) {
		for _, span := range spanMap {
			diag.UpdateSpanStatusFromGRPCError(span, err)
			span.End()
		}
	}

	features := thepubsub.Features()
	entryIdSet := make(map[string]struct{}, len(in.Entries)) //nolint:stylecheck

	entries := make([]pubsub.BulkMessageEntry, len(in.Entries))
	for i, entry := range in.Entries {
		// Validate entry_id
		if _, ok := entryIdSet[entry.EntryId]; ok || entry.EntryId == "" {
			err := status.Errorf(codes.InvalidArgument, messages.ErrPubsubMarshal, in.Topic, in.PubsubName, "entryId is duplicated or not present for entry")
			apiServerLogger.Debug(err)
			return &runtimev1pb.BulkPublishResponse{}, err
		}
		entryIdSet[entry.EntryId] = struct{}{}
		entries[i].EntryId = entry.EntryId
		entries[i].ContentType = entry.ContentType
		entries[i].Event = entry.Event
		// Populate entry metadata with request level metadata. Entry level metadata keys
		// override request level metadata.
		if entry.Metadata != nil {
			entries[i].Metadata = utils.PopulateMetadataForBulkPublishEntry(in.Metadata, entry.Metadata)
		}

		if !rawPayload {
			// For multiple events in a single bulk call traceParent is different for each event.
			_, childSpan := diag.StartGRPCProducerSpanChildFromParent(ctx, span, "/dapr.proto.runtime.v1.Dapr/BulkPublishEventAlpha1/")
			// Populate W3C traceparent to cloudevent envelope
			corID := diag.SpanContextToW3CString(childSpan.SpanContext())
			spanMap[i] = childSpan

			var envelope map[string]interface{}

			envelope, err := runtimePubsub.NewCloudEvent(&runtimePubsub.CloudEvent{
				ID:              a.id,
				Topic:           topic,
				DataContentType: entries[i].ContentType,
				Data:            entries[i].Event,
				TraceID:         corID,
				TraceState:      traceState,
				Pubsub:          pubsubName,
			})
			if err != nil {
				err = status.Errorf(codes.InvalidArgument, messages.ErrPubsubCloudEventCreation, err.Error())
				apiServerLogger.Debug(err)
				closeChildSpans(ctx, err)
				return &runtimev1pb.BulkPublishResponse{}, err
			}

			pubsub.ApplyMetadata(envelope, features, entries[i].Metadata)

			entries[i].Event, err = json.Marshal(envelope)
			if err != nil {
				err = status.Errorf(codes.InvalidArgument, messages.ErrPubsubCloudEventsSer, topic, pubsubName, err.Error())
				apiServerLogger.Debug(err)
				closeChildSpans(ctx, err)
				return &runtimev1pb.BulkPublishResponse{}, err
			}
		}
	}

	req := pubsub.BulkPublishRequest{
		PubsubName: pubsubName,
		Topic:      topic,
		Entries:    entries,
		Metadata:   in.Metadata,
	}

	start := time.Now()
	// err is only nil if all entries are successfully published.
	// For partial success, err is not nil and res contains the failed entries.
	res, err := a.pubsubAdapter.BulkPublish(&req)

	elapsed := diag.ElapsedSince(start)
	eventsPublished := int64(len(req.Entries))

	if len(res.FailedEntries) != 0 {
		eventsPublished -= int64(len(res.FailedEntries))
	}
	diag.DefaultComponentMonitoring.BulkPubsubEgressEvent(context.Background(), pubsubName, topic, err == nil, eventsPublished, elapsed)

	// BulkPublishResponse contains all failed entries from the request.
	// If there are no failed entries, then the failedEntries array will be empty.
	bulkRes := runtimev1pb.BulkPublishResponse{}

	if err != nil {
		// Only respond with error if it is  permission denied or not found.
		// On error, the response will be empty.
		nerr := status.Errorf(codes.Internal, messages.ErrPubsubPublishMessage, topic, pubsubName, err.Error())
		if errors.As(err, &runtimePubsub.NotAllowedError{}) {
			nerr = status.Errorf(codes.PermissionDenied, err.Error())
		}

		if errors.As(err, &runtimePubsub.NotFoundError{}) {
			nerr = status.Errorf(codes.NotFound, err.Error())
		}
		apiServerLogger.Debug(nerr)
		closeChildSpans(ctx, nerr)
		return &bulkRes, nerr
	}

	bulkRes.FailedEntries = make([]*runtimev1pb.BulkPublishResponseFailedEntry, 0, len(res.FailedEntries))
	for _, r := range res.FailedEntries {
		resEntry := runtimev1pb.BulkPublishResponseFailedEntry{EntryId: r.EntryId}
		if r.Error != nil {
			resEntry.Error = r.Error.Error()
		}
		bulkRes.FailedEntries = append(bulkRes.FailedEntries, &resEntry)
	}
	closeChildSpans(ctx, nil)
	// even on partial failures, err is nil. As when error is set, the response is expected to not be processed.
	return &bulkRes, nil
}

func (a *api) InvokeBinding(ctx context.Context, in *runtimev1pb.InvokeBindingRequest) (*runtimev1pb.InvokeBindingResponse, error) {
	req := &bindings.InvokeRequest{
		Metadata:  make(map[string]string, len(in.Metadata)),
		Operation: bindings.OperationKind(in.Operation),
		Data:      in.Data,
	}
	for key, val := range in.Metadata {
		req.Metadata[key] = val
	}

	// Allow for distributed tracing by passing context metadata.
	if incomingMD, ok := metadata.FromIncomingContext(ctx); ok {
		for key, val := range incomingMD {
			sanitizedKey := invokev1.ReservedGRPCMetadataToDaprPrefixHeader(key)
			req.Metadata[sanitizedKey] = val[0]
		}
	}

	r := &runtimev1pb.InvokeBindingResponse{}
	start := time.Now()
	resp, err := a.sendToOutputBindingFn(in.Name, req)
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.OutputBindingEvent(context.Background(), in.Name, in.Operation, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrInvokeOutputBinding, in.Name, err.Error())
		apiServerLogger.Debug(err)
		return r, err
	}

	if resp != nil {
		r.Data = resp.Data
		r.Metadata = resp.Metadata
	}
	return r, nil
}

type bulkGetRes struct {
	bulkGet   bool
	responses []state.BulkGetResponse
}

func (a *api) GetBulkState(ctx context.Context, in *runtimev1pb.GetBulkStateRequest) (*runtimev1pb.GetBulkStateResponse, error) {
	bulkResp := &runtimev1pb.GetBulkStateResponse{}
	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return bulkResp, err
	}

	if len(in.Keys) == 0 {
		return bulkResp, nil
	}

	// try bulk get first
	reqs := make([]state.GetRequest, len(in.Keys))
	for i, k := range in.Keys {
		key, err1 := stateLoader.GetModifiedStateKey(k, in.StoreName, a.id)
		if err1 != nil {
			return &runtimev1pb.GetBulkStateResponse{}, err1
		}
		r := state.GetRequest{
			Key:      key,
			Metadata: in.Metadata,
		}
		reqs[i] = r
	}

	start := time.Now()
	policyDef := a.resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore)
	bgrPolicyRunner := resiliency.NewRunner[*bulkGetRes](ctx, policyDef)
	bgr, err := bgrPolicyRunner(func(ctx context.Context) (*bulkGetRes, error) {
		rBulkGet, rBulkResponse, rErr := store.BulkGet(ctx, reqs)
		return &bulkGetRes{
			bulkGet:   rBulkGet,
			responses: rBulkResponse,
		}, rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.BulkGet, err == nil, elapsed)

	if bgr == nil {
		bgr = &bulkGetRes{}
	}

	// if store supports bulk get
	if bgr.bulkGet {
		if err != nil {
			return bulkResp, err
		}
		for i := 0; i < len(bgr.responses); i++ {
			item := &runtimev1pb.BulkStateItem{
				Key:      stateLoader.GetOriginalStateKey(bgr.responses[i].Key),
				Data:     bgr.responses[i].Data,
				Etag:     stringValueOrEmpty(bgr.responses[i].ETag),
				Metadata: bgr.responses[i].Metadata,
				Error:    bgr.responses[i].Error,
			}
			bulkResp.Items = append(bulkResp.Items, item)
		}
		return bulkResp, nil
	}

	// if store doesn't support bulk get, fallback to call get() method one by one
	limiter := concurrency.NewLimiter(int(in.Parallelism))
	n := len(reqs)
	resultCh := make(chan *runtimev1pb.BulkStateItem, n)
	for i := 0; i < n; i++ {
		fn := func(param interface{}) {
			req := param.(*state.GetRequest)
			policyRunner := resiliency.NewRunner[*state.GetResponse](ctx, policyDef)
			res, policyErr := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
				return store.Get(ctx, req)
			})

			item := &runtimev1pb.BulkStateItem{
				Key: stateLoader.GetOriginalStateKey(req.Key),
			}

			if policyErr != nil {
				item.Error = policyErr.Error()
			} else if res != nil {
				item.Data = res.Data
				item.Etag = stringValueOrEmpty(res.ETag)
				item.Metadata = res.Metadata
			}
			resultCh <- item
		}
		limiter.Execute(fn, &reqs[i])
	}
	limiter.Wait()
	// collect result
	for i := 0; i < n; i++ {
		item := <-resultCh

		if encryption.EncryptedStateStore(in.StoreName) {
			val, err := encryption.TryDecryptValue(in.StoreName, item.Data)
			if err != nil {
				item.Error = err.Error()
				apiServerLogger.Debug(err)

				continue
			}

			item.Data = val
		}

		bulkResp.Items = append(bulkResp.Items, item)
	}
	return bulkResp, nil
}

func (a *api) getStateStore(name string) (state.Store, error) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		return nil, status.Error(codes.FailedPrecondition, messages.ErrStateStoresNotConfigured)
	}

	if a.stateStores[name] == nil {
		return nil, status.Errorf(codes.InvalidArgument, messages.ErrStateStoreNotFound, name)
	}
	return a.stateStores[name], nil
}

func (a *api) getComponent(componentKind string, componentName string) (component interface{}) {
	switch componentKind {
	case "state":
		if statestore := a.stateStores[componentName]; statestore != nil {
			component = statestore
		}
	case "pubsub":
		if pubsub := a.pubsubAdapter.GetPubSub(componentName); pubsub != nil {
			component = pubsub
		}
	case "secretstores":
		if secretstore := a.secretStores[componentName]; secretstore != nil {
			component = secretstore
		}
	case "bindings":
		if inputbinding := a.inputBindings[componentName]; inputbinding != nil {
			component = inputbinding
		} else if outputbinding := a.outputBindings[componentName]; outputbinding != nil {
			component = outputbinding
		}
	}
	return
}

// GetComponentHealthAlpha1 returns the health of components.
// If a componentName is specified, it returns the health of that component.
// If no componentName is specified, it returns the health of all components.
func (a *api) GetComponentHealthAlpha1(ctx context.Context, in *runtimev1pb.ComponentHealthRequest) (*runtimev1pb.ComponentHealthResponse, error) {
	componentName := in.ComponentName
	components := a.getComponentsFn()
	if componentName == nil || *componentName == "" {
		return a.getAllComponentsHealth(ctx)
	}
	for _, comp := range components {
		if *componentName == comp.Name {
			status, _, _, err := a.checkHealthUtil(strings.Split(comp.Spec.Type, ".")[0], comp.Name)
			return &runtimev1pb.ComponentHealthResponse{
				Results: []*runtimev1pb.ComponentHealthResponseItem{
					{
						Status: &status,
					},
				},
			}, err
		}
	}
	err := status.Errorf(codes.InvalidArgument, messages.ErrComponentNotFound)
	apiServerLogger.Debug(err)

	undefinedStatus := utils.StatusUndefined
	errCompNotFound := messages.ErrComponentNotFound
	return &runtimev1pb.ComponentHealthResponse{
		Results: []*runtimev1pb.ComponentHealthResponseItem{
			{
				Status:    &undefinedStatus,
				ErrorCode: &errCompNotFound,
			},
		},
	}, err
}

func (a *api) getAllComponentsHealth(ctx context.Context) (*runtimev1pb.ComponentHealthResponse, error) {
	components := a.getComponentsFn()
	hresp := &runtimev1pb.ComponentHealthResponse{
		Results: make([]*runtimev1pb.ComponentHealthResponseItem, len(components)),
	}
	for i, comp := range components {
		a.componentsHealthResponsePopulator(strings.Split(comp.Spec.Type, ".")[0], hresp, i, comp.Name)
	}
	return hresp, nil
}

func (a *api) componentsHealthResponsePopulator(componentType string, hresp *runtimev1pb.ComponentHealthResponse, ind int, name string) {
	status, errStr, message, _ := a.checkHealthUtil(componentType, name)
	item := &runtimev1pb.ComponentHealthResponseItem{
		ComponentName: &name,
		Type:          &componentType,
		Status:        &status,
	}
	if errStr != "" {
		item.ErrorCode = &errStr
	}
	if message != "" {
		item.Message = &message
	}
	hresp.Results[ind] = item
}

func (a *api) checkHealthUtil(componentKind string, componentName string) (
	string, string, string, error,
) {
	component := a.getComponent(componentKind, componentName)

	if component == nil {
		err := status.Errorf(codes.InvalidArgument, messages.ErrComponentNotFound)
		apiServerLogger.Debug(err)

		return utils.StatusUndefined, messages.ErrComponentNotFound, "", err
	}

	if pinger, ok := component.(health.Pinger); ok {
		pingErr := pinger.Ping()
		if pingErr != nil {
			err := status.Errorf(codes.Unknown, messages.ErrHealthNotOk)
			apiServerLogger.Debug(pingErr)

			return utils.StatusNotOk, messages.ErrHealthNotOk, pingErr.Error(), err
		}
		return utils.StatusOk, "", "", nil
	}
	err := status.Errorf(codes.Unimplemented, messages.ErrPingNotImplemented)
	return utils.StatusUndefined, messages.ErrPingNotImplemented, "", err
}

func (a *api) GetState(ctx context.Context, in *runtimev1pb.GetStateRequest) (*runtimev1pb.GetStateResponse, error) {
	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetStateResponse{}, err
	}
	key, err := stateLoader.GetModifiedStateKey(in.Key, in.StoreName, a.id)
	if err != nil {
		return &runtimev1pb.GetStateResponse{}, err
	}
	req := &state.GetRequest{
		Key:      key,
		Metadata: in.Metadata,
		Options: state.GetStateOption{
			Consistency: stateConsistencyToString(in.Consistency),
		},
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*state.GetResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore),
	)
	getResponse, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
		return store.Get(ctx, req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.Get, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrStateGet, in.Key, in.StoreName, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetStateResponse{}, err
	}

	if getResponse == nil {
		getResponse = &state.GetResponse{}
	}
	if encryption.EncryptedStateStore(in.StoreName) {
		val, err := encryption.TryDecryptValue(in.StoreName, getResponse.Data)
		if err != nil {
			err = status.Errorf(codes.Internal, messages.ErrStateGet, in.Key, in.StoreName, err.Error())
			apiServerLogger.Debug(err)
			return &runtimev1pb.GetStateResponse{}, err
		}

		getResponse.Data = val
	}

	response := &runtimev1pb.GetStateResponse{}
	if getResponse != nil {
		response.Etag = stringValueOrEmpty(getResponse.ETag)
		response.Data = getResponse.Data
		response.Metadata = getResponse.Metadata
	}
	return response, nil
}

func (a *api) SaveState(ctx context.Context, in *runtimev1pb.SaveStateRequest) (*emptypb.Empty, error) {
	empty := &emptypb.Empty{}

	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return empty, err
	}

	reqs := []state.SetRequest{}
	for _, s := range in.States {
		key, err1 := stateLoader.GetModifiedStateKey(s.Key, in.StoreName, a.id)
		if err1 != nil {
			return empty, err1
		}
		req := state.SetRequest{
			Key:      key,
			Metadata: s.Metadata,
		}

		if contentType, ok := req.Metadata[contribMetadata.ContentType]; ok && contentType == contenttype.JSONContentType {
			if err1 = json.Unmarshal(s.Value, &req.Value); err1 != nil {
				return empty, err1
			}
		} else {
			req.Value = s.Value
		}

		if s.Etag != nil {
			req.ETag = &s.Etag.Value
		}
		if s.Options != nil {
			req.Options = state.SetStateOption{
				Consistency: stateConsistencyToString(s.Options.Consistency),
				Concurrency: stateConcurrencyToString(s.Options.Concurrency),
			}
		}
		if encryption.EncryptedStateStore(in.StoreName) {
			val, encErr := encryption.TryEncryptValue(in.StoreName, s.Value)
			if encErr != nil {
				apiServerLogger.Debug(encErr)
				return empty, encErr
			}

			req.Value = val
		}

		reqs = append(reqs, req)
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.BulkSet(ctx, reqs)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.Set, err == nil, elapsed)

	if err != nil {
		err = a.stateErrorResponse(err, messages.ErrStateSave, in.StoreName, err.Error())
		apiServerLogger.Debug(err)
		return empty, err
	}
	return empty, nil
}

func (a *api) QueryStateAlpha1(ctx context.Context, in *runtimev1pb.QueryStateRequest) (*runtimev1pb.QueryStateResponse, error) {
	ret := &runtimev1pb.QueryStateResponse{}

	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return ret, err
	}

	querier, ok := store.(state.Querier)
	if !ok {
		err = status.Errorf(codes.Unimplemented, messages.ErrNotFound, "Query")
		apiServerLogger.Debug(err)
		return ret, err
	}

	if encryption.EncryptedStateStore(in.StoreName) {
		err = status.Errorf(codes.Aborted, messages.ErrStateQuery, in.GetStoreName(), "cannot query encrypted store")
		apiServerLogger.Debug(err)
		return ret, err
	}

	var req state.QueryRequest
	if err = json.Unmarshal([]byte(in.GetQuery()), &req.Query); err != nil {
		err = status.Errorf(codes.InvalidArgument, messages.ErrMalformedRequest, err.Error())
		apiServerLogger.Debug(err)
		return ret, err
	}
	req.Metadata = in.GetMetadata()

	start := time.Now()
	policyRunner := resiliency.NewRunner[*state.QueryResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore),
	)
	resp, err := policyRunner(func(ctx context.Context) (*state.QueryResponse, error) {
		return querier.Query(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.StateQuery, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrStateQuery, in.GetStoreName(), err.Error())
		apiServerLogger.Debug(err)
		return ret, err
	}

	if resp == nil || len(resp.Results) == 0 {
		return ret, nil
	}

	ret.Results = make([]*runtimev1pb.QueryStateItem, len(resp.Results))
	ret.Token = resp.Token
	ret.Metadata = resp.Metadata

	for i := range resp.Results {
		ret.Results[i] = &runtimev1pb.QueryStateItem{
			Key:  stateLoader.GetOriginalStateKey(resp.Results[i].Key),
			Data: resp.Results[i].Data,
		}
	}

	return ret, nil
}

// stateErrorResponse takes a state store error, format and args and returns a status code encoded gRPC error.
func (a *api) stateErrorResponse(err error, format string, args ...interface{}) error {
	e, ok := err.(*state.ETagError)
	if !ok {
		return status.Errorf(codes.Internal, format, args...)
	}
	switch e.Kind() {
	case state.ETagMismatch:
		return status.Errorf(codes.Aborted, format, args...)
	case state.ETagInvalid:
		return status.Errorf(codes.InvalidArgument, format, args...)
	}

	return status.Errorf(codes.Internal, format, args...)
}

func (a *api) DeleteState(ctx context.Context, in *runtimev1pb.DeleteStateRequest) (*emptypb.Empty, error) {
	empty := &emptypb.Empty{}

	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return empty, err
	}

	key, err := stateLoader.GetModifiedStateKey(in.Key, in.StoreName, a.id)
	if err != nil {
		return empty, err
	}
	req := state.DeleteRequest{
		Key:      key,
		Metadata: in.Metadata,
	}
	if in.Etag != nil {
		req.ETag = &in.Etag.Value
	}
	if in.Options != nil {
		req.Options = state.DeleteStateOption{
			Concurrency: stateConcurrencyToString(in.Options.Concurrency),
			Consistency: stateConsistencyToString(in.Options.Consistency),
		}
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.Delete(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.Delete, err == nil, elapsed)

	if err != nil {
		err = a.stateErrorResponse(err, messages.ErrStateDelete, in.Key, err.Error())
		apiServerLogger.Debug(err)
		return empty, err
	}
	return empty, nil
}

func (a *api) DeleteBulkState(ctx context.Context, in *runtimev1pb.DeleteBulkStateRequest) (*emptypb.Empty, error) {
	empty := &emptypb.Empty{}

	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return empty, err
	}

	reqs := make([]state.DeleteRequest, 0, len(in.States))
	for _, item := range in.States {
		key, err1 := stateLoader.GetModifiedStateKey(item.Key, in.StoreName, a.id)
		if err1 != nil {
			return empty, err1
		}
		req := state.DeleteRequest{
			Key:      key,
			Metadata: item.Metadata,
		}
		if item.Etag != nil {
			req.ETag = &item.Etag.Value
		}
		if item.Options != nil {
			req.Options = state.DeleteStateOption{
				Concurrency: stateConcurrencyToString(item.Options.Concurrency),
				Consistency: stateConsistencyToString(item.Options.Consistency),
			}
		}
		reqs = append(reqs, req)
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.BulkDelete(ctx, reqs)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.BulkDelete, err == nil, elapsed)

	if err != nil {
		apiServerLogger.Debug(err)
		return empty, err
	}
	return empty, nil
}

func (a *api) GetSecret(ctx context.Context, in *runtimev1pb.GetSecretRequest) (*runtimev1pb.GetSecretResponse, error) {
	response := &runtimev1pb.GetSecretResponse{}

	if a.secretStores == nil || len(a.secretStores) == 0 {
		err := status.Error(codes.FailedPrecondition, messages.ErrSecretStoreNotConfigured)
		apiServerLogger.Debug(err)
		return response, err
	}

	secretStoreName := in.StoreName

	if a.secretStores[secretStoreName] == nil {
		err := status.Errorf(codes.InvalidArgument, messages.ErrSecretStoreNotFound, secretStoreName)
		apiServerLogger.Debug(err)
		return response, err
	}

	if !a.isSecretAllowed(in.StoreName, in.Key) {
		err := status.Errorf(codes.PermissionDenied, messages.ErrPermissionDenied, in.Key, in.StoreName)
		apiServerLogger.Debug(err)
		return response, err
	}

	req := secretstores.GetSecretRequest{
		Name:     in.Key,
		Metadata: in.Metadata,
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*secretstores.GetSecretResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(secretStoreName, resiliency.Secretstore),
	)
	getResponse, err := policyRunner(func(ctx context.Context) (*secretstores.GetSecretResponse, error) {
		rResp, rErr := a.secretStores[secretStoreName].GetSecret(ctx, req)
		return &rResp, rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.SecretInvoked(ctx, in.StoreName, diag.Get, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrSecretGet, req.Name, secretStoreName, err.Error())
		apiServerLogger.Debug(err)
		return response, err
	}

	if getResponse != nil {
		response.Data = getResponse.Data
	}
	return response, nil
}

func (a *api) GetBulkSecret(ctx context.Context, in *runtimev1pb.GetBulkSecretRequest) (*runtimev1pb.GetBulkSecretResponse, error) {
	response := &runtimev1pb.GetBulkSecretResponse{}

	if a.secretStores == nil || len(a.secretStores) == 0 {
		err := status.Error(codes.FailedPrecondition, messages.ErrSecretStoreNotConfigured)
		apiServerLogger.Debug(err)
		return response, err
	}

	secretStoreName := in.StoreName

	if a.secretStores[secretStoreName] == nil {
		err := status.Errorf(codes.InvalidArgument, messages.ErrSecretStoreNotFound, secretStoreName)
		apiServerLogger.Debug(err)
		return response, err
	}

	req := secretstores.BulkGetSecretRequest{
		Metadata: in.Metadata,
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*secretstores.BulkGetSecretResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(secretStoreName, resiliency.Secretstore),
	)
	getResponse, err := policyRunner(func(ctx context.Context) (*secretstores.BulkGetSecretResponse, error) {
		rResp, rErr := a.secretStores[secretStoreName].BulkGetSecret(ctx, req)
		return &rResp, rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.SecretInvoked(ctx, in.StoreName, diag.BulkGet, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrBulkSecretGet, secretStoreName, err.Error())
		apiServerLogger.Debug(err)
		return response, err
	}

	if getResponse == nil {
		return response, nil
	}
	filteredSecrets := map[string]map[string]string{}
	for key, v := range getResponse.Data {
		if a.isSecretAllowed(secretStoreName, key) {
			filteredSecrets[key] = v
		} else {
			apiServerLogger.Debugf(messages.ErrPermissionDenied, key, in.StoreName)
		}
	}

	if getResponse.Data != nil {
		response.Data = map[string]*runtimev1pb.SecretResponse{}
		for key, v := range filteredSecrets {
			response.Data[key] = &runtimev1pb.SecretResponse{Secrets: v}
		}
	}
	return response, nil
}

func extractEtag(req *commonv1pb.StateItem) (bool, string) {
	if req.Etag != nil {
		return true, req.Etag.Value
	}
	return false, ""
}

func (a *api) ExecuteStateTransaction(ctx context.Context, in *runtimev1pb.ExecuteStateTransactionRequest) (*emptypb.Empty, error) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		err := status.Error(codes.FailedPrecondition, messages.ErrStateStoresNotConfigured)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	if a.stateStores[in.StoreName] == nil {
		err := status.Errorf(codes.InvalidArgument, messages.ErrStateStoreNotFound, in.StoreName)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	transactionalStore, ok := a.transactionalStateStores[in.StoreName]
	if !ok {
		err := status.Errorf(codes.Unimplemented, messages.ErrStateStoreNotSupported, in.StoreName)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	operations := make([]state.TransactionalStateOperation, len(in.Operations))
	for i, inputReq := range in.Operations {
		req := inputReq.Request

		hasEtag, etag := extractEtag(req)
		key, err := stateLoader.GetModifiedStateKey(req.Key, in.StoreName, a.id)
		if err != nil {
			return &emptypb.Empty{}, err
		}
		switch state.OperationType(inputReq.OperationType) {
		case state.Upsert:
			setReq := state.SetRequest{
				Key: key,
				// Limitation:
				// components that cannot handle byte array need to deserialize/serialize in
				// component specific way in components-contrib repo.
				Value:    req.Value,
				Metadata: req.Metadata,
			}

			if hasEtag {
				setReq.ETag = &etag
			}
			if req.Options != nil {
				setReq.Options = state.SetStateOption{
					Concurrency: stateConcurrencyToString(req.Options.Concurrency),
					Consistency: stateConsistencyToString(req.Options.Consistency),
				}
			}

			operations[i] = state.TransactionalStateOperation{
				Operation: state.Upsert,
				Request:   setReq,
			}

		case state.Delete:
			delReq := state.DeleteRequest{
				Key:      key,
				Metadata: req.Metadata,
			}

			if hasEtag {
				delReq.ETag = &etag
			}
			if req.Options != nil {
				delReq.Options = state.DeleteStateOption{
					Concurrency: stateConcurrencyToString(req.Options.Concurrency),
					Consistency: stateConsistencyToString(req.Options.Consistency),
				}
			}

			operations[i] = state.TransactionalStateOperation{
				Operation: state.Delete,
				Request:   delReq,
			}

		default:
			err := status.Errorf(codes.Unimplemented, messages.ErrNotSupportedStateOperation, inputReq.OperationType)
			apiServerLogger.Debug(err)
			return &emptypb.Empty{}, err
		}
	}

	if encryption.EncryptedStateStore(in.StoreName) {
		for i, op := range operations {
			if op.Operation == state.Upsert {
				req := op.Request.(*state.SetRequest)
				data := []byte(fmt.Sprintf("%v", req.Value))
				val, err := encryption.TryEncryptValue(in.StoreName, data)
				if err != nil {
					err = status.Errorf(codes.Internal, messages.ErrStateTransaction, err.Error())
					apiServerLogger.Debug(err)
					return &emptypb.Empty{}, err
				}

				req.Value = val
				operations[i].Request = req
			}
		}
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore),
	)
	storeReq := &state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   in.Metadata,
	}
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, transactionalStore.Multi(ctx, storeReq)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.StateTransaction, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrStateTransaction, err.Error())
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}
	return &emptypb.Empty{}, nil
}

func (a *api) RegisterActorTimer(ctx context.Context, in *runtimev1pb.RegisterActorTimerRequest) (*emptypb.Empty, error) {
	if a.actor == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	req := &actors.CreateTimerRequest{
		Name:      in.Name,
		ActorID:   in.ActorId,
		ActorType: in.ActorType,
		DueTime:   in.DueTime,
		Period:    in.Period,
		TTL:       in.Ttl,
		Callback:  in.Callback,
	}

	if in.Data != nil {
		req.Data = in.Data
	}
	err := a.actor.CreateTimer(ctx, req)
	return &emptypb.Empty{}, err
}

func (a *api) UnregisterActorTimer(ctx context.Context, in *runtimev1pb.UnregisterActorTimerRequest) (*emptypb.Empty, error) {
	if a.actor == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	req := &actors.DeleteTimerRequest{
		Name:      in.Name,
		ActorID:   in.ActorId,
		ActorType: in.ActorType,
	}

	err := a.actor.DeleteTimer(ctx, req)
	return &emptypb.Empty{}, err
}

func (a *api) RegisterActorReminder(ctx context.Context, in *runtimev1pb.RegisterActorReminderRequest) (*emptypb.Empty, error) {
	if a.actor == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	req := &actors.CreateReminderRequest{
		Name:      in.Name,
		ActorID:   in.ActorId,
		ActorType: in.ActorType,
		DueTime:   in.DueTime,
		Period:    in.Period,
		TTL:       in.Ttl,
	}

	if in.Data != nil {
		req.Data = in.Data
	}
	err := a.actor.CreateReminder(ctx, req)
	return &emptypb.Empty{}, err
}

func (a *api) UnregisterActorReminder(ctx context.Context, in *runtimev1pb.UnregisterActorReminderRequest) (*emptypb.Empty, error) {
	if a.actor == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	req := &actors.DeleteReminderRequest{
		Name:      in.Name,
		ActorID:   in.ActorId,
		ActorType: in.ActorType,
	}

	err := a.actor.DeleteReminder(ctx, req)
	return &emptypb.Empty{}, err
}

func (a *api) RenameActorReminder(ctx context.Context, in *runtimev1pb.RenameActorReminderRequest) (*emptypb.Empty, error) {
	if a.actor == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	req := &actors.RenameReminderRequest{
		OldName:   in.OldName,
		ActorID:   in.ActorId,
		ActorType: in.ActorType,
		NewName:   in.NewName,
	}

	err := a.actor.RenameReminder(ctx, req)
	return &emptypb.Empty{}, err
}

func (a *api) GetActorState(ctx context.Context, in *runtimev1pb.GetActorStateRequest) (*runtimev1pb.GetActorStateResponse, error) {
	if a.actor == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return nil, err
	}

	actorType := in.ActorType
	actorID := in.ActorId
	key := in.Key

	hosted := a.actor.IsActorHosted(ctx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		err := status.Errorf(codes.Internal, messages.ErrActorInstanceMissing)
		apiServerLogger.Debug(err)
		return nil, err
	}

	req := actors.GetStateRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Key:       key,
	}

	resp, err := a.actor.GetState(ctx, &req)
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrActorStateGet, err))
		apiServerLogger.Debug(err)
		return nil, err
	}

	return &runtimev1pb.GetActorStateResponse{
		Data: resp.Data,
	}, nil
}

func (a *api) ExecuteActorStateTransaction(ctx context.Context, in *runtimev1pb.ExecuteActorStateTransactionRequest) (*emptypb.Empty, error) {
	if a.actor == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	actorType := in.ActorType
	actorID := in.ActorId
	actorOps := []actors.TransactionalOperation{}

	for _, op := range in.Operations {
		var actorOp actors.TransactionalOperation
		switch state.OperationType(op.OperationType) {
		case state.Upsert:
			setReq := map[string]interface{}{
				"key":   op.Key,
				"value": op.Value.Value,
				// Actor state do not user other attributes from state request.
			}

			actorOp = actors.TransactionalOperation{
				Operation: actors.Upsert,
				Request:   setReq,
			}
		case state.Delete:
			delReq := map[string]interface{}{
				"key": op.Key,
				// Actor state do not user other attributes from state request.
			}

			actorOp = actors.TransactionalOperation{
				Operation: actors.Delete,
				Request:   delReq,
			}

		default:
			err := status.Errorf(codes.Unimplemented, messages.ErrNotSupportedStateOperation, op.OperationType)
			apiServerLogger.Debug(err)
			return &emptypb.Empty{}, err
		}

		actorOps = append(actorOps, actorOp)
	}

	hosted := a.actor.IsActorHosted(ctx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		err := status.Errorf(codes.Internal, messages.ErrActorInstanceMissing)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	req := actors.TransactionalRequest{
		ActorID:    actorID,
		ActorType:  actorType,
		Operations: actorOps,
	}

	err := a.actor.TransactionalStateOperation(ctx, &req)
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrActorStateTransactionSave, err))
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	return &emptypb.Empty{}, nil
}

func (a *api) InvokeActor(ctx context.Context, in *runtimev1pb.InvokeActorRequest) (*runtimev1pb.InvokeActorResponse, error) {
	response := &runtimev1pb.InvokeActorResponse{}

	if a.actor == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return response, err
	}

	policyDef := a.resiliency.ActorPreLockPolicy(in.ActorType, in.ActorId)

	reqMetadata := make(map[string][]string, len(in.Metadata))
	for k, v := range in.Metadata {
		reqMetadata[k] = []string{v}
	}
	req := invokev1.NewInvokeMethodRequest(in.Method).
		WithActor(in.ActorType, in.ActorId).
		WithRawDataBytes(in.Data).
		WithMetadata(reqMetadata).
		WithReplay(policyDef.HasRetries())
	defer req.Close()

	// Unlike other actor calls, resiliency is handled here for invocation.
	// This is due to actor invocation involving a lookup for the host.
	// Having the retry here allows us to capture that and be resilient to host failure.
	// Additionally, we don't perform timeouts at this level. This is because an actor
	// should technically wait forever on the locking mechanism. If we timeout while
	// waiting for the lock, we can also create a queue of calls that will try and continue
	// after the timeout.
	policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef,
		resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
			Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
		},
	)
	resp, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return a.actor.Call(ctx, req)
	})
	if err != nil && !errors.Is(err, actors.ErrDaprResponseHeader) {
		err = status.Errorf(codes.Internal, messages.ErrActorInvoke, err)
		apiServerLogger.Debug(err)
		return response, err
	}

	if resp == nil {
		resp = invokev1.NewInvokeMethodResponse(500, "Blank request", nil)
	}
	defer resp.Close()

	response.Data, err = resp.RawDataFull()
	if err != nil {
		return nil, fmt.Errorf("failed to read response data: %w", err)
	}
	return response, nil
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

func (a *api) GetMetadata(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.GetMetadataResponse, error) {
	extendedMetadata := make(map[string]string)
	// Copy synchronously so it can be serialized to JSON.
	a.extendedMetadata.Range(func(key, value interface{}) bool {
		extendedMetadata[key.(string)] = value.(string)
		return true
	})
	extendedMetadata[daprRuntimeVersionKey] = a.daprRunTimeVersion

	activeActorsCount := []*runtimev1pb.ActiveActorsCount{}
	if a.actor != nil {
		for _, actorTypeCount := range a.actor.GetActiveActorsCount(ctx) {
			activeActorsCount = append(activeActorsCount, &runtimev1pb.ActiveActorsCount{
				Type:  actorTypeCount.Type,
				Count: int32(actorTypeCount.Count),
			})
		}
	}

	components := a.getComponentsFn()
	registeredComponents := make([]*runtimev1pb.RegisteredComponents, 0, len(components))
	componentsCapabilities := a.getComponentsCapabilitesFn()
	for _, comp := range components {
		registeredComp := &runtimev1pb.RegisteredComponents{
			Name:         comp.Name,
			Version:      comp.Spec.Version,
			Type:         comp.Spec.Type,
			Capabilities: getOrDefaultCapabilities(componentsCapabilities, comp.Name),
		}
		registeredComponents = append(registeredComponents, registeredComp)
	}

	subscriptions, err := a.getSubscriptionsFn()
	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrPubsubGetSubscriptions, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetMetadataResponse{}, err
	}
	ps := []*runtimev1pb.PubsubSubscription{}
	for _, s := range subscriptions {
		ps = append(ps, &runtimev1pb.PubsubSubscription{
			PubsubName:      s.PubsubName,
			Topic:           s.Topic,
			Metadata:        s.Metadata,
			DeadLetterTopic: s.DeadLetterTopic,
			Rules:           convertPubsubSubscriptionRules(s.Rules),
		})
	}

	response := &runtimev1pb.GetMetadataResponse{
		Id:                   a.id,
		ExtendedMetadata:     extendedMetadata,
		RegisteredComponents: registeredComponents,
		ActiveActorsCount:    activeActorsCount,
		Subscriptions:        ps,
	}

	return response, nil
}

func getOrDefaultCapabilities(dict map[string][]string, key string) []string {
	if val, ok := dict[key]; ok {
		return val
	}
	return make([]string, 0)
}

func convertPubsubSubscriptionRules(rules []*runtimePubsub.Rule) *runtimev1pb.PubsubSubscriptionRules {
	out := &runtimev1pb.PubsubSubscriptionRules{
		Rules: make([]*runtimev1pb.PubsubSubscriptionRule, 0),
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, &runtimev1pb.PubsubSubscriptionRule{
			Match: fmt.Sprintf("%s", r.Match),
			Path:  r.Path,
		})
	}
	return out
}

// SetMetadata Sets value in extended metadata of the sidecar.
func (a *api) SetMetadata(ctx context.Context, in *runtimev1pb.SetMetadataRequest) (*emptypb.Empty, error) {
	a.extendedMetadata.Store(in.Key, in.Value)
	return &emptypb.Empty{}, nil
}

func (a *api) GetWorkflowAlpha1(ctx context.Context, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowResponse, error) {
	if in.InstanceId == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrMissingOrEmptyInstance)
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}
	if in.WorkflowComponent == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrNoOrMissingWorkflowComponent)
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}
	workflowRun := a.workflowComponents[in.WorkflowComponent]
	if workflowRun == nil {
		err := status.Errorf(codes.InvalidArgument, fmt.Sprintf(messages.ErWorkflowrComponentDoesNotExist, in.WorkflowComponent))
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}
	req := workflows.WorkflowReference{
		InstanceID: in.InstanceId,
	}
	response, err := workflowRun.Get(ctx, &req)
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrWorkflowGetResponse, err))
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}

	id := &runtimev1pb.WorkflowReference{
		InstanceId: response.WFInfo.InstanceID,
	}

	t, err := time.Parse(time.RFC3339, response.StartTime)
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrTimerParse, err))
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}

	res := &runtimev1pb.GetWorkflowResponse{
		InstanceId: id.InstanceId,
		StartTime:  t.Unix(),
		Metadata:   response.Metadata,
	}
	return res, nil
}

func (a *api) StartWorkflowAlpha1(ctx context.Context, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.WorkflowReference, error) {
	if in.WorkflowName == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrWorkflowNameMissing)
		apiServerLogger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}

	if in.WorkflowComponent == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrNoOrMissingWorkflowComponent)
		apiServerLogger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}

	if in.InstanceId == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrMissingOrEmptyInstance)
		apiServerLogger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}

	workflowRun := a.workflowComponents[in.WorkflowComponent]
	if workflowRun == nil {
		err := status.Errorf(codes.InvalidArgument, fmt.Sprintf(messages.ErWorkflowrComponentDoesNotExist, in.WorkflowComponent))
		apiServerLogger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}

	wf := workflows.WorkflowReference{
		InstanceID: in.InstanceId,
	}
	req := workflows.StartRequest{
		WorkflowReference: wf,
		Options:           in.Options,
		WorkflowName:      in.WorkflowName,
		Input:             in.Input,
	}

	resp, err := workflowRun.Start(ctx, &req)
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrStartWorkflow, err))
		apiServerLogger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}
	ret := &runtimev1pb.WorkflowReference{
		InstanceId: resp.InstanceID,
	}
	return ret, nil
}

func (a *api) TerminateWorkflowAlpha1(ctx context.Context, in *runtimev1pb.TerminateWorkflowRequest) (*runtimev1pb.TerminateWorkflowResponse, error) {
	if in.InstanceId == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrMissingOrEmptyInstance)
		apiServerLogger.Debug(err)
		return &runtimev1pb.TerminateWorkflowResponse{}, err
	}

	if in.WorkflowComponent == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrNoOrMissingWorkflowComponent)
		apiServerLogger.Debug(err)
		return &runtimev1pb.TerminateWorkflowResponse{}, err
	}

	workflowRun := a.workflowComponents[in.WorkflowComponent]
	if workflowRun == nil {
		err := status.Errorf(codes.InvalidArgument, fmt.Sprintf(messages.ErWorkflowrComponentDoesNotExist, in.WorkflowComponent))
		apiServerLogger.Debug(err)
		return &runtimev1pb.TerminateWorkflowResponse{}, err
	}

	req := workflows.WorkflowReference{
		InstanceID: in.InstanceId,
	}

	err := workflowRun.Terminate(ctx, &req)
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrTerminateWorkflow, err))
		apiServerLogger.Debug(err)
		return &runtimev1pb.TerminateWorkflowResponse{}, nil
	}
	return &runtimev1pb.TerminateWorkflowResponse{}, nil
}

// Shutdown the sidecar.
func (a *api) Shutdown(ctx context.Context, in *emptypb.Empty) (*emptypb.Empty, error) {
	go func() {
		<-ctx.Done()
		a.shutdown()
	}()
	return &emptypb.Empty{}, nil
}

func stringValueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func (a *api) getConfigurationStore(name string) (configuration.Store, error) {
	if a.configurationStores == nil || len(a.configurationStores) == 0 {
		return nil, status.Error(codes.FailedPrecondition, messages.ErrConfigurationStoresNotConfigured)
	}

	if a.configurationStores[name] == nil {
		return nil, status.Errorf(codes.InvalidArgument, messages.ErrConfigurationStoreNotFound, name)
	}
	return a.configurationStores[name], nil
}

func (a *api) GetConfigurationAlpha1(ctx context.Context, in *runtimev1pb.GetConfigurationRequest) (*runtimev1pb.GetConfigurationResponse, error) {
	response := &runtimev1pb.GetConfigurationResponse{}

	store, err := a.getConfigurationStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return response, err
	}

	req := configuration.GetRequest{
		Keys:     in.Keys,
		Metadata: in.Metadata,
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*configuration.GetResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Configuration),
	)
	getResponse, err := policyRunner(func(ctx context.Context) (*configuration.GetResponse, error) {
		return store.Get(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(ctx, in.StoreName, diag.Get, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrConfigurationGet, req.Keys, in.StoreName, err.Error())
		apiServerLogger.Debug(err)
		return response, err
	}

	if getResponse != nil {
		cachedItems := make(map[string]*commonv1pb.ConfigurationItem, len(getResponse.Items))
		for k, v := range getResponse.Items {
			cachedItems[k] = &commonv1pb.ConfigurationItem{
				Metadata: v.Metadata,
				Value:    v.Value,
				Version:  v.Version,
			}
		}
		response.Items = cachedItems
	}

	return response, nil
}

type configurationEventHandler struct {
	api          *api
	storeName    string
	serverStream runtimev1pb.Dapr_SubscribeConfigurationAlpha1Server //nolint:nosnakecase
}

func (h *configurationEventHandler) updateEventHandler(ctx context.Context, e *configuration.UpdateEvent) error {
	items := make(map[string]*commonv1pb.ConfigurationItem, len(e.Items))
	for k, v := range e.Items {
		items[k] = &commonv1pb.ConfigurationItem{
			Value:    v.Value,
			Version:  v.Version,
			Metadata: v.Metadata,
		}
	}

	if err := h.serverStream.Send(&runtimev1pb.SubscribeConfigurationResponse{
		Items: items,
		Id:    e.ID,
	}); err != nil {
		apiServerLogger.Debug(err)
		return err
	}
	return nil
}

func (a *api) SubscribeConfigurationAlpha1(request *runtimev1pb.SubscribeConfigurationRequest, configurationServer runtimev1pb.Dapr_SubscribeConfigurationAlpha1Server) error { //nolint:nosnakecase
	store, err := a.getConfigurationStore(request.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return err
	}
	sort.Slice(request.Keys, func(i, j int) bool {
		return request.Keys[i] < request.Keys[j]
	})

	subscribeKeys := make([]string, 0)

	// TODO(@halspang) provide a switch to use just resiliency or this.

	if len(request.Keys) > 0 {
		subscribeKeys = append(subscribeKeys, request.Keys...)
	}

	req := &configuration.SubscribeRequest{
		Keys:     subscribeKeys,
		Metadata: request.GetMetadata(),
	}

	handler := &configurationEventHandler{
		api:          a,
		storeName:    request.StoreName,
		serverStream: configurationServer,
	}

	// TODO(@laurence) deal with failed subscription and retires
	start := time.Now()
	policyRunner := resiliency.NewRunner[string](configurationServer.Context(),
		a.resiliency.ComponentOutboundPolicy(request.StoreName, resiliency.Configuration),
	)
	subscribeID, err := policyRunner(func(ctx context.Context) (string, error) {
		return store.Subscribe(ctx, req, handler.updateEventHandler)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), request.StoreName, diag.ConfigurationSubscribe, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.InvalidArgument, messages.ErrConfigurationSubscribe, req.Keys, request.StoreName, err.Error())
		apiServerLogger.Debug(err)
		return err
	}
	if err := handler.serverStream.Send(&runtimev1pb.SubscribeConfigurationResponse{
		Id: subscribeID,
	}); err != nil {
		apiServerLogger.Debug(err)
		return err
	}

	stop := make(chan struct{})
	a.configurationSubscribeLock.Lock()
	a.configurationSubscribe[subscribeID] = stop
	a.configurationSubscribeLock.Unlock()
	<-stop

	return nil
}

func (a *api) UnsubscribeConfigurationAlpha1(ctx context.Context, request *runtimev1pb.UnsubscribeConfigurationRequest) (*runtimev1pb.UnsubscribeConfigurationResponse, error) {
	store, err := a.getConfigurationStore(request.GetStoreName())
	if err != nil {
		apiServerLogger.Debug(err)
		return &runtimev1pb.UnsubscribeConfigurationResponse{
			Ok:      false,
			Message: err.Error(),
		}, err
	}

	a.configurationSubscribeLock.Lock()
	defer a.configurationSubscribeLock.Unlock()

	subscribeID := request.GetId()

	stop, ok := a.configurationSubscribe[subscribeID]
	if !ok {
		return &runtimev1pb.UnsubscribeConfigurationResponse{
			Ok: true,
		}, nil
	}
	delete(a.configurationSubscribe, subscribeID)
	close(stop)

	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(request.StoreName, resiliency.Configuration),
	)

	start := time.Now()
	storeReq := &configuration.UnsubscribeRequest{
		ID: subscribeID,
	}
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.Unsubscribe(ctx, storeReq)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), request.StoreName, diag.ConfigurationUnsubscribe, err == nil, elapsed)

	if err != nil {
		return &runtimev1pb.UnsubscribeConfigurationResponse{
			Ok:      false,
			Message: err.Error(),
		}, err
	}
	return &runtimev1pb.UnsubscribeConfigurationResponse{
		Ok: true,
	}, nil
}

func (a *api) Close() error {
	a.configurationSubscribeLock.Lock()
	defer a.configurationSubscribeLock.Unlock()

	for k, stop := range a.configurationSubscribe {
		close(stop)
		delete(a.configurationSubscribe, k)
	}

	return nil
}
