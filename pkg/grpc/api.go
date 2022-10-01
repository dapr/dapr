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
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapr/components-contrib/lock"
	lockLoader "github.com/dapr/dapr/pkg/components/lock"
	"github.com/dapr/dapr/pkg/version"

	"github.com/dapr/components-contrib/configuration"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/contenttype"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/acl"
	"github.com/dapr/dapr/pkg/actors"
	componentsV1alpha "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/channel"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

const (
	daprHTTPStatusHeader  = "dapr-http-status"
	daprRuntimeVersionKey = "daprRuntimeVersion"
)

// API is the gRPC interface for the Dapr gRPC API. It implements both the internal and external proto definitions.
//
//nolint:nosnakecase
type API interface {
	// DaprInternal Service methods
	CallActor(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)

	// Dapr Service methods
	PublishEvent(ctx context.Context, in *runtimev1pb.PublishEventRequest) (*emptypb.Empty, error)
	InvokeService(ctx context.Context, in *runtimev1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error)
	InvokeBinding(ctx context.Context, in *runtimev1pb.InvokeBindingRequest) (*runtimev1pb.InvokeBindingResponse, error)
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
	SetAppChannel(appChannel channel.AppChannel)
	SetDirectMessaging(directMessaging messaging.DirectMessaging)
	SetActorRuntime(actor actors.Actors)
	RegisterActorTimer(ctx context.Context, in *runtimev1pb.RegisterActorTimerRequest) (*emptypb.Empty, error)
	UnregisterActorTimer(ctx context.Context, in *runtimev1pb.UnregisterActorTimerRequest) (*emptypb.Empty, error)
	RegisterActorReminder(ctx context.Context, in *runtimev1pb.RegisterActorReminderRequest) (*emptypb.Empty, error)
	UnregisterActorReminder(ctx context.Context, in *runtimev1pb.UnregisterActorReminderRequest) (*emptypb.Empty, error)
	RenameActorReminder(ctx context.Context, in *runtimev1pb.RenameActorReminderRequest) (*emptypb.Empty, error)
	GetActorState(ctx context.Context, in *runtimev1pb.GetActorStateRequest) (*runtimev1pb.GetActorStateResponse, error)
	ExecuteActorStateTransaction(ctx context.Context, in *runtimev1pb.ExecuteActorStateTransactionRequest) (*emptypb.Empty, error)
	InvokeActor(ctx context.Context, in *runtimev1pb.InvokeActorRequest) (*runtimev1pb.InvokeActorResponse, error)
	TryLockAlpha1(ctx context.Context, in *runtimev1pb.TryLockRequest) (*runtimev1pb.TryLockResponse, error)
	UnlockAlpha1(ctx context.Context, in *runtimev1pb.UnlockRequest) (*runtimev1pb.UnlockResponse, error)
	// Gets metadata of the sidecar
	GetMetadata(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.GetMetadataResponse, error)
	// Sets value in extended metadata of the sidecar
	SetMetadata(ctx context.Context, in *runtimev1pb.SetMetadataRequest) (*emptypb.Empty, error)
	// Shutdown the sidecar
	Shutdown(ctx context.Context, in *emptypb.Empty) (*emptypb.Empty, error)
}

type api struct {
	actor                      actors.Actors
	directMessaging            messaging.DirectMessaging
	appChannel                 channel.AppChannel
	resiliency                 resiliency.Provider
	stateStores                map[string]state.Store
	transactionalStateStores   map[string]state.TransactionalStore
	secretStores               map[string]secretstores.SecretStore
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
	compResp, err := store.TryLock(compReq)
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
	compResp, err := store.Unlock(compReq)
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

// NewAPI returns a new gRPC API.
func NewAPI(
	appID string, appChannel channel.AppChannel,
	resiliency resiliency.Provider,
	stateStores map[string]state.Store,
	secretStores map[string]secretstores.SecretStore,
	secretsConfiguration map[string]config.SecretsScope,
	configurationStores map[string]configuration.Store,
	lockStores map[string]lock.Store,
	pubsubAdapter runtimePubsub.Adapter,
	directMessaging messaging.DirectMessaging,
	actor actors.Actors,
	sendToOutputBindingFn func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error),
	tracingSpec config.TracingSpec,
	accessControlList *config.AccessControlList,
	appProtocol string,
	getComponentsFn func() []componentsV1alpha.Component,
	shutdown func(),
	getComponentsCapabilitiesFn func() map[string][]string,
) API {
	transactionalStateStores := map[string]state.TransactionalStore{}
	for key, store := range stateStores {
		if state.FeatureTransactional.IsPresent(store.Features()) {
			transactionalStateStores[key] = store.(state.TransactionalStore)
		}
	}
	return &api{
		directMessaging:            directMessaging,
		actor:                      actor,
		id:                         appID,
		resiliency:                 resiliency,
		appChannel:                 appChannel,
		pubsubAdapter:              pubsubAdapter,
		stateStores:                stateStores,
		transactionalStateStores:   transactionalStateStores,
		secretStores:               secretStores,
		configurationStores:        configurationStores,
		configurationSubscribe:     make(map[string]chan struct{}),
		lockStores:                 lockStores,
		secretsConfiguration:       secretsConfiguration,
		sendToOutputBindingFn:      sendToOutputBindingFn,
		tracingSpec:                tracingSpec,
		accessControlList:          accessControlList,
		appProtocol:                appProtocol,
		shutdown:                   shutdown,
		getComponentsFn:            getComponentsFn,
		getComponentsCapabilitesFn: getComponentsCapabilitiesFn,
		daprRunTimeVersion:         version.Version(),
	}
}

// CallLocal is used for internal dapr to dapr calls. It is invoked by another Dapr instance with a request to the local app.
func (a *api) CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	if a.appChannel == nil {
		return nil, status.Error(codes.Internal, messages.ErrChannelNotFound)
	}

	req, err := invokev1.InternalInvokeRequest(in)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, messages.ErrInternalInvokeRequest, err.Error())
	}

	if a.accessControlList != nil {
		// An access control policy has been specified for the app. Apply the policies.
		operation := req.Message().Method
		var httpVerb commonv1pb.HTTPExtension_Verb //nolint:nosnakecase
		// Get the http verb in case the application protocol is http
		if a.appProtocol == config.HTTPProtocol && req.Metadata() != nil && len(req.Metadata()) > 0 {
			httpExt := req.Message().GetHttpExtension()
			if httpExt != nil {
				httpVerb = httpExt.GetVerb()
			}
		}
		callAllowed, errMsg := acl.ApplyAccessControlPolicies(ctx, operation, httpVerb, a.appProtocol, a.accessControlList)

		if !callAllowed {
			return nil, status.Errorf(codes.PermissionDenied, errMsg)
		}
	}

	resp, err := a.appChannel.InvokeMethod(ctx, req)
	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrChannelInvoke, err)
		return nil, err
	}
	return resp.Proto(), err
}

// CallActor invokes a virtual actor.
func (a *api) CallActor(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	req, err := invokev1.InternalInvokeRequest(in)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, messages.ErrInternalInvokeRequest, err.Error())
	}

	// We don't do resiliency here as it is handled in the API layer. See InvokeActor().
	resp, err := a.actor.Call(ctx, req)
	if err != nil {
		// We have to remove the error to keep the body, so callers must re-inspect for the header in the actual response.
		if errors.Is(err, actors.ErrDaprResponseHeader) {
			return resp.Proto(), nil
		}

		err = status.Errorf(codes.Internal, messages.ErrActorInvoke, err)
		return nil, err
	}
	return resp.Proto(), nil
}

func (a *api) PublishEvent(ctx context.Context, in *runtimev1pb.PublishEventRequest) (*emptypb.Empty, error) {
	if a.pubsubAdapter == nil {
		err := status.Error(codes.FailedPrecondition, messages.ErrPubsubNotConfigured)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	pubsubName := in.PubsubName
	if pubsubName == "" {
		err := status.Error(codes.InvalidArgument, messages.ErrPubsubEmpty)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	thepubsub := a.pubsubAdapter.GetPubSub(pubsubName)
	if thepubsub == nil {
		err := status.Errorf(codes.InvalidArgument, messages.ErrPubsubNotFound, pubsubName)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	topic := in.Topic
	if topic == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrTopicEmpty, pubsubName)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	rawPayload, metaErr := contribMetadata.IsRawPayload(in.Metadata)
	if metaErr != nil {
		err := status.Errorf(codes.InvalidArgument, messages.ErrMetadataGet, metaErr.Error())
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
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

func (a *api) InvokeService(ctx context.Context, in *runtimev1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error) {
	req := invokev1.FromInvokeRequestMessage(in.GetMessage())

	if incomingMD, ok := metadata.FromIncomingContext(ctx); ok {
		req.WithMetadata(incomingMD)
	}

	if a.directMessaging == nil {
		return nil, status.Errorf(codes.Internal, messages.ErrDirectInvokeNotReady)
	}

	policy := a.resiliency.EndpointPolicy(ctx, in.Id, fmt.Sprintf("%s:%s", in.Id, req.Message().Method))
	var resp *invokev1.InvokeMethodResponse
	respError := policy(func(ctx context.Context) (rErr error) {
		resp, rErr = a.directMessaging.Invoke(ctx, in.Id, req)
		if rErr != nil {
			rErr = status.Errorf(codes.Internal, messages.ErrDirectInvoke, in.Id, rErr)
			return rErr
		}

		headerMD := invokev1.InternalMetadataToGrpcMetadata(ctx, resp.Headers(), true)

		// If the status is OK, respError will be nil.
		var respError error
		if resp.IsHTTPResponse() {
			errorMessage := []byte{}
			if resp != nil {
				_, errorMessage = resp.RawData()
			}
			respError = invokev1.ErrorFromHTTPResponseCode(int(resp.Status().Code), string(errorMessage))
			// Populate http status code to header
			headerMD.Set(daprHTTPStatusHeader, strconv.Itoa(int(resp.Status().Code)))
		} else {
			respError = invokev1.ErrorFromInternalStatus(resp.Status())
			// ignore trailer if appchannel uses HTTP
			grpc.SetTrailer(ctx, invokev1.InternalMetadataToGrpcMetadata(ctx, resp.Trailers(), false))
		}

		grpc.SetHeader(ctx, headerMD)

		return respError
	})

	// In this case, there was an error with the actual request or a resiliency policy stopped the request.
	if respError != nil {
		// Check if it's returned by status.Errorf
		_, ok := respError.(interface{ GRPCStatus() *status.Status })
		if ok || (errors.Is(respError, context.DeadlineExceeded) || breaker.IsErrorPermanent(respError)) {
			return nil, respError
		}
	}
	return resp.Message(), respError
}

func (a *api) InvokeBinding(ctx context.Context, in *runtimev1pb.InvokeBindingRequest) (*runtimev1pb.InvokeBindingResponse, error) {
	req := &bindings.InvokeRequest{
		Metadata:  make(map[string]string),
		Operation: bindings.OperationKind(in.Operation),
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

	if in.Data != nil {
		req.Data = in.Data
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

func (a *api) GetBulkState(ctx context.Context, in *runtimev1pb.GetBulkStateRequest) (*runtimev1pb.GetBulkStateResponse, error) {
	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetBulkStateResponse{}, err
	}

	bulkResp := &runtimev1pb.GetBulkStateResponse{}
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
	var bulkGet bool
	var responses []state.BulkGetResponse
	policy := a.resiliency.ComponentOutboundPolicy(ctx, in.StoreName, resiliency.Statestore)
	err = policy(func(ctx context.Context) (rErr error) {
		bulkGet, responses, rErr = store.BulkGet(reqs)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.BulkGet, err == nil, elapsed)

	// if store supports bulk get
	if bulkGet {
		if err != nil {
			return bulkResp, err
		}
		for i := 0; i < len(responses); i++ {
			item := &runtimev1pb.BulkStateItem{
				Key:      stateLoader.GetOriginalStateKey(responses[i].Key),
				Data:     responses[i].Data,
				Etag:     stringValueOrEmpty(responses[i].ETag),
				Metadata: responses[i].Metadata,
				Error:    responses[i].Error,
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
			var r *state.GetResponse
			ok := atomic.Bool{}
			policyErr := policy(func(ctx context.Context) error {
				res, rErr := store.Get(req)
				if rErr != nil {
					return rErr
				}
				if ok.CompareAndSwap(false, true) {
					r = res
				}
				return nil
			})

			item := &runtimev1pb.BulkStateItem{
				Key: stateLoader.GetOriginalStateKey(req.Key),
			}
			if policyErr != nil {
				item.Error = policyErr.Error()
			} else if r != nil {
				item.Data = r.Data
				item.Etag = stringValueOrEmpty(r.ETag)
				item.Metadata = r.Metadata
			}
			resultCh <- item
		}
		limiter.Execute(fn, &reqs[i])
	}
	limiter.Wait()
	// collect result
	resultLen := len(resultCh)
	for i := 0; i < resultLen; i++ {
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
	req := state.GetRequest{
		Key:      key,
		Metadata: in.Metadata,
		Options: state.GetStateOption{
			Consistency: stateConsistencyToString(in.Consistency),
		},
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(ctx, in.StoreName, resiliency.Statestore)
	var getResponse *state.GetResponse
	err = policy(func(ctx context.Context) (rErr error) {
		getResponse, rErr = store.Get(&req)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.Get, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrStateGet, in.Key, in.StoreName, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetStateResponse{}, err
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
	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	reqs := []state.SetRequest{}
	for _, s := range in.States {
		key, err1 := stateLoader.GetModifiedStateKey(s.Key, in.StoreName, a.id)
		if err1 != nil {
			return &emptypb.Empty{}, err1
		}
		req := state.SetRequest{
			Key:      key,
			Metadata: s.Metadata,
		}

		if contentType, ok := req.Metadata[contribMetadata.ContentType]; ok && contentType == contenttype.JSONContentType {
			if err1 = json.Unmarshal(s.Value, &req.Value); err1 != nil {
				return &emptypb.Empty{}, err1
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
				return &emptypb.Empty{}, encErr
			}

			req.Value = val
		}

		reqs = append(reqs, req)
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(ctx, in.StoreName, resiliency.Statestore)
	err = policy(func(ctx context.Context) error {
		return store.BulkSet(reqs)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.Set, err == nil, elapsed)

	if err != nil {
		err = a.stateErrorResponse(err, messages.ErrStateSave, in.StoreName, err.Error())
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}
	return &emptypb.Empty{}, nil
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
	policy := a.resiliency.ComponentOutboundPolicy(ctx, in.StoreName, resiliency.Statestore)
	var resp *state.QueryResponse
	err = policy(func(ctx context.Context) (rErr error) {
		resp, rErr = querier.Query(&req)
		return rErr
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
	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	key, err := stateLoader.GetModifiedStateKey(in.Key, in.StoreName, a.id)
	if err != nil {
		return &emptypb.Empty{}, err
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
	policy := a.resiliency.ComponentOutboundPolicy(ctx, in.StoreName, resiliency.Statestore)
	err = policy(func(ctx context.Context) error {
		return store.Delete(&req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.Delete, err == nil, elapsed)

	if err != nil {
		err = a.stateErrorResponse(err, messages.ErrStateDelete, in.Key, err.Error())
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}
	return &emptypb.Empty{}, nil
}

func (a *api) DeleteBulkState(ctx context.Context, in *runtimev1pb.DeleteBulkStateRequest) (*emptypb.Empty, error) {
	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	reqs := make([]state.DeleteRequest, 0, len(in.States))
	for _, item := range in.States {
		key, err1 := stateLoader.GetModifiedStateKey(item.Key, in.StoreName, a.id)
		if err1 != nil {
			return &emptypb.Empty{}, err1
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
	policy := a.resiliency.ComponentOutboundPolicy(ctx, in.StoreName, resiliency.Statestore)
	err = policy(func(ctx context.Context) error {
		return store.BulkDelete(reqs)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.BulkDelete, err == nil, elapsed)

	if err != nil {
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}
	return &emptypb.Empty{}, nil
}

func (a *api) GetSecret(ctx context.Context, in *runtimev1pb.GetSecretRequest) (*runtimev1pb.GetSecretResponse, error) {
	if a.secretStores == nil || len(a.secretStores) == 0 {
		err := status.Error(codes.FailedPrecondition, messages.ErrSecretStoreNotConfigured)
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetSecretResponse{}, err
	}

	secretStoreName := in.StoreName

	if a.secretStores[secretStoreName] == nil {
		err := status.Errorf(codes.InvalidArgument, messages.ErrSecretStoreNotFound, secretStoreName)
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetSecretResponse{}, err
	}

	if !a.isSecretAllowed(in.StoreName, in.Key) {
		err := status.Errorf(codes.PermissionDenied, messages.ErrPermissionDenied, in.Key, in.StoreName)
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetSecretResponse{}, err
	}

	req := secretstores.GetSecretRequest{
		Name:     in.Key,
		Metadata: in.Metadata,
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(ctx, secretStoreName, resiliency.Secretstore)
	var getResponse secretstores.GetSecretResponse
	err := policy(func(ctx context.Context) (rErr error) {
		getResponse, rErr = a.secretStores[secretStoreName].GetSecret(ctx, req)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.SecretInvoked(ctx, in.StoreName, diag.Get, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrSecretGet, req.Name, secretStoreName, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetSecretResponse{}, err
	}

	response := &runtimev1pb.GetSecretResponse{}
	if getResponse.Data != nil {
		response.Data = getResponse.Data
	}
	return response, nil
}

func (a *api) GetBulkSecret(ctx context.Context, in *runtimev1pb.GetBulkSecretRequest) (*runtimev1pb.GetBulkSecretResponse, error) {
	if a.secretStores == nil || len(a.secretStores) == 0 {
		err := status.Error(codes.FailedPrecondition, messages.ErrSecretStoreNotConfigured)
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetBulkSecretResponse{}, err
	}

	secretStoreName := in.StoreName

	if a.secretStores[secretStoreName] == nil {
		err := status.Errorf(codes.InvalidArgument, messages.ErrSecretStoreNotFound, secretStoreName)
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetBulkSecretResponse{}, err
	}

	req := secretstores.BulkGetSecretRequest{
		Metadata: in.Metadata,
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(ctx, secretStoreName, resiliency.Secretstore)
	var getResponse secretstores.BulkGetSecretResponse
	err := policy(func(ctx context.Context) (rErr error) {
		getResponse, rErr = a.secretStores[secretStoreName].BulkGetSecret(ctx, req)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.SecretInvoked(ctx, in.StoreName, diag.BulkGet, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrBulkSecretGet, secretStoreName, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetBulkSecretResponse{}, err
	}

	filteredSecrets := map[string]map[string]string{}
	for key, v := range getResponse.Data {
		if a.isSecretAllowed(secretStoreName, key) {
			filteredSecrets[key] = v
		} else {
			apiServerLogger.Debugf(messages.ErrPermissionDenied, key, in.StoreName)
		}
	}

	response := &runtimev1pb.GetBulkSecretResponse{}
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

	storeName := in.StoreName

	if a.stateStores[storeName] == nil {
		err := status.Errorf(codes.InvalidArgument, messages.ErrStateStoreNotFound, storeName)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	transactionalStore, ok := a.transactionalStateStores[storeName]
	if !ok {
		err := status.Errorf(codes.Unimplemented, messages.ErrStateStoreNotSupported, storeName)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	operations := []state.TransactionalStateOperation{}
	for _, inputReq := range in.Operations {
		var operation state.TransactionalStateOperation
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

			operation = state.TransactionalStateOperation{
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

			operation = state.TransactionalStateOperation{
				Operation: state.Delete,
				Request:   delReq,
			}

		default:
			err := status.Errorf(codes.Unimplemented, messages.ErrNotSupportedStateOperation, inputReq.OperationType)
			apiServerLogger.Debug(err)
			return &emptypb.Empty{}, err
		}

		operations = append(operations, operation)
	}

	if encryption.EncryptedStateStore(storeName) {
		for i, op := range operations {
			if op.Operation == state.Upsert {
				req := op.Request.(*state.SetRequest)
				data := []byte(fmt.Sprintf("%v", req.Value))
				val, err := encryption.TryEncryptValue(storeName, data)
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
	policy := a.resiliency.ComponentOutboundPolicy(ctx, in.StoreName, resiliency.Statestore)
	err := policy(func(ctx context.Context) error {
		return transactionalStore.Multi(&state.TransactionalStateRequest{
			Operations: operations,
			Metadata:   in.Metadata,
		})
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
	if a.actor == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return &runtimev1pb.InvokeActorResponse{}, err
	}

	req := invokev1.NewInvokeMethodRequest(in.Method)
	req.WithActor(in.ActorType, in.ActorId)
	req.WithRawData(in.Data, "")
	reqMetadata := map[string][]string{}
	for k, v := range in.Metadata {
		reqMetadata[k] = []string{v}
	}
	req.WithMetadata(reqMetadata)

	// Unlike other actor calls, resiliency is handled here for invocation.
	// This is due to actor invocation involving a lookup for the host.
	// Having the retry here allows us to capture that and be resilient to host failure.
	// Additionally, we don't perform timeouts at this level. This is because an actor
	// should technically wait forever on the locking mechanism. If we timeout while
	// waiting for the lock, we can also create a queue of calls that will try and continue
	// after the timeout.
	resp := invokev1.NewInvokeMethodResponse(500, "Blank request", nil)
	policy := a.resiliency.ActorPreLockPolicy(ctx, in.ActorType, in.ActorId)
	err := policy(func(ctx context.Context) (rErr error) {
		resp, rErr = a.actor.Call(ctx, req)
		return rErr
	})
	if err != nil && !errors.Is(err, actors.ErrDaprResponseHeader) {
		err = status.Errorf(codes.Internal, messages.ErrActorInvoke, err)
		apiServerLogger.Debug(err)
		return &runtimev1pb.InvokeActorResponse{}, err
	}

	_, body := resp.RawData()
	return &runtimev1pb.InvokeActorResponse{
		Data: body,
	}, nil
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

	response := &runtimev1pb.GetMetadataResponse{
		Id:                   a.id,
		ExtendedMetadata:     extendedMetadata,
		RegisteredComponents: registeredComponents,
		ActiveActorsCount:    activeActorsCount,
	}

	return response, nil
}

func getOrDefaultCapabilities(dict map[string][]string, key string) []string {
	if val, ok := dict[key]; ok {
		return val
	}
	return make([]string, 0)
}

// SetMetadata Sets value in extended metadata of the sidecar.
func (a *api) SetMetadata(ctx context.Context, in *runtimev1pb.SetMetadataRequest) (*emptypb.Empty, error) {
	a.extendedMetadata.Store(in.Key, in.Value)
	return &emptypb.Empty{}, nil
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
	store, err := a.getConfigurationStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetConfigurationResponse{}, err
	}

	req := configuration.GetRequest{
		Keys:     in.Keys,
		Metadata: in.Metadata,
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(ctx, in.StoreName, resiliency.Configuration)
	var getResponse *configuration.GetResponse
	err = policy(func(ctx context.Context) (rErr error) {
		getResponse, rErr = store.Get(ctx, &req)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(ctx, in.StoreName, diag.Get, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrConfigurationGet, req.Keys, in.StoreName, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetConfigurationResponse{}, err
	}

	cachedItems := make(map[string]*commonv1pb.ConfigurationItem, len(getResponse.Items))
	for k, v := range getResponse.Items {
		cachedItems[k] = &commonv1pb.ConfigurationItem{
			Metadata: v.Metadata,
			Value:    v.Value,
			Version:  v.Version,
		}
	}

	response := &runtimev1pb.GetConfigurationResponse{
		Items: cachedItems,
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
	newCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// empty list means subscribing to all configuration keys
	if len(request.Keys) == 0 {
		getConfigurationReq := &runtimev1pb.GetConfigurationRequest{
			StoreName: request.StoreName,
			Keys:      []string{},
			Metadata:  request.GetMetadata(),
		}
		resp, err2 := a.GetConfigurationAlpha1(newCtx, getConfigurationReq)
		if err2 != nil {
			err2 = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrConfigurationGet, request.Keys, request.StoreName, err2))
			apiServerLogger.Debug(err2)
			return err2
		}

		items := resp.GetItems()
		for key := range items {
			if _, ok := a.configurationSubscribe[fmt.Sprintf("%s||%s", request.StoreName, key)]; !ok {
				subscribeKeys = append(subscribeKeys, key)
			}
		}
	} else {
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
	policy := a.resiliency.ComponentOutboundPolicy(newCtx, request.StoreName, resiliency.Configuration)
	var id string
	err = policy(func(ctx context.Context) (rErr error) {
		id, rErr = store.Subscribe(ctx, req, handler.updateEventHandler)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(context.Background(), request.StoreName, diag.ConfigurationSubscribe, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.InvalidArgument, messages.ErrConfigurationSubscribe, req.Keys, request.StoreName, err.Error())
		apiServerLogger.Debug(err)
		return err
	}
	if err := handler.serverStream.Send(&runtimev1pb.SubscribeConfigurationResponse{
		Id: id,
	}); err != nil {
		apiServerLogger.Debug(err)
		return err
	}
	stop := make(chan struct{})
	a.configurationSubscribeLock.Lock()
	a.configurationSubscribe[id] = stop
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

	policy := a.resiliency.ComponentOutboundPolicy(ctx, request.StoreName, resiliency.Configuration)

	start := time.Now()
	err = policy(func(ctx context.Context) error {
		return store.Unsubscribe(ctx, &configuration.UnsubscribeRequest{
			ID: subscribeID,
		})
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
