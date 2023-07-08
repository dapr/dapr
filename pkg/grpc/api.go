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

	otelTrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/components-contrib/contenttype"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/channel"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/grpc/metadata"
	"github.com/dapr/dapr/pkg/grpc/universalapi"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/utils"
)

const daprHTTPStatusHeader = "dapr-http-status"

// API is the gRPC interface for the Dapr gRPC API. It implements both the internal and external proto definitions.
type API interface {
	// DaprInternal Service methods
	internalv1pb.ServiceInvocationServer

	// Dapr Service methods
	runtimev1pb.DaprServer

	// Methods internal to the object
	SetAppChannel(appChannel channel.AppChannel)
	SetDirectMessaging(directMessaging messaging.DirectMessaging)
	SetActorRuntime(actor actors.Actors)
}

type api struct {
	*universalapi.UniversalAPI
	directMessaging       messaging.DirectMessaging
	appChannel            channel.AppChannel
	resiliency            resiliency.Provider
	pubsubAdapter         runtimePubsub.Adapter
	sendToOutputBindingFn func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	tracingSpec           config.TracingSpec
	accessControlList     *config.AccessControlList
}

// APIOpts contains options for NewAPI.
type APIOpts struct {
	AppID                       string
	AppChannel                  channel.AppChannel
	Resiliency                  resiliency.Provider
	CompStore                   *compstore.ComponentStore
	PubsubAdapter               runtimePubsub.Adapter
	DirectMessaging             messaging.DirectMessaging
	Actors                      actors.Actors
	SendToOutputBindingFn       func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	TracingSpec                 config.TracingSpec
	AccessControlList           *config.AccessControlList
	Shutdown                    func()
	GetComponentsCapabilitiesFn func() map[string][]string
	AppConnectionConfig         config.AppConnectionConfig
	GlobalConfig                *config.Configuration
}

// NewAPI returns a new gRPC API.
func NewAPI(opts APIOpts) API {
	return &api{
		UniversalAPI: &universalapi.UniversalAPI{
			AppID:                      opts.AppID,
			Logger:                     apiServerLogger,
			Resiliency:                 opts.Resiliency,
			Actors:                     opts.Actors,
			CompStore:                  opts.CompStore,
			ShutdownFn:                 opts.Shutdown,
			GetComponentsCapabilitesFn: opts.GetComponentsCapabilitiesFn,
			AppConnectionConfig:        opts.AppConnectionConfig,
			GlobalConfig:               opts.GlobalConfig,
		},
		directMessaging:       opts.DirectMessaging,
		resiliency:            opts.Resiliency,
		appChannel:            opts.AppChannel,
		pubsubAdapter:         opts.PubsubAdapter,
		sendToOutputBindingFn: opts.SendToOutputBindingFn,
		tracingSpec:           opts.TracingSpec,
		accessControlList:     opts.AccessControlList,
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
		return nil, "", "", false, messages.ErrPubSubMetadataDeserialize.WithFormat(metaErr)
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
			Source:          a.UniversalAPI.AppID,
			Topic:           in.Topic,
			DataContentType: in.DataContentType,
			Data:            body,
			TraceID:         corID,
			TraceState:      traceState,
			Pubsub:          in.PubsubName,
		}, in.Metadata)
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

// These flags are used to make sure that we are printing the deprecation warning log messages in "InvokeService" just once.
// By using "CompareAndSwap(false, true)" we replace the value "false" with "true" only if it's not already "true".
// "CompareAndSwap" returns true if the swap happened (i.e. if the value was not already "true"), so we can use that as a flag to make sure we only run the code once.
// Why not using "sync.Once"? In our tests (https://github.com/dapr/dapr/pull/5740), that seems to be causing a regression in the perf tests. This is probably because when using "sync.Once" and the callback needs to be executed for the first time, all concurrent requests are blocked too. Additionally, the use of closures in this case _could_ have an impact on the GC as well.
var (
	invokeServiceDeprecationNoticeShown     = atomic.Bool{}
	invokeServiceHTTPDeprecationNoticeShown = atomic.Bool{}
)

// Deprecated: Use proxy mode service invocation instead.
func (a *api) InvokeService(ctx context.Context, in *runtimev1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error) {
	if a.directMessaging == nil {
		return nil, status.Errorf(codes.Internal, messages.ErrDirectInvokeNotReady)
	}

	if invokeServiceDeprecationNoticeShown.CompareAndSwap(false, true) {
		apiServerLogger.Warn("[DEPRECATION NOTICE] InvokeService is deprecated and will be removed in the future, please use proxy mode instead.")
	}
	policyDef := a.resiliency.EndpointPolicy(in.Id, in.Id+":"+in.Message.Method)

	req := invokev1.FromInvokeRequestMessage(in.GetMessage())
	if policyDef != nil {
		req.WithReplay(policyDef.HasRetries())
	}
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
			if invokeServiceHTTPDeprecationNoticeShown.CompareAndSwap(false, true) {
				apiServerLogger.Warn("[DEPRECATION NOTICE] Invocation path of gRPC -> HTTP is deprecated and will be removed in the future.")
			}
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

			envelope, err := runtimePubsub.NewCloudEvent(&runtimePubsub.CloudEvent{
				Source:          a.UniversalAPI.AppID,
				Topic:           topic,
				DataContentType: entries[i].ContentType,
				Data:            entries[i].Event,
				TraceID:         corID,
				TraceState:      traceState,
				Pubsub:          pubsubName,
			}, entries[i].Metadata)
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

func (a *api) GetBulkState(ctx context.Context, in *runtimev1pb.GetBulkStateRequest) (*runtimev1pb.GetBulkStateResponse, error) {
	bulkResp := &runtimev1pb.GetBulkStateResponse{}
	store, err := a.UniversalAPI.GetStateStore(in.StoreName)
	if err != nil {
		// Error has already been logged
		return bulkResp, err
	}

	if len(in.Keys) == 0 {
		return bulkResp, nil
	}

	var key string
	reqs := make([]state.GetRequest, len(in.Keys))
	for i, k := range in.Keys {
		key, err = stateLoader.GetModifiedStateKey(k, in.StoreName, a.UniversalAPI.AppID)
		if err != nil {
			return &runtimev1pb.GetBulkStateResponse{}, err
		}
		r := state.GetRequest{
			Key:      key,
			Metadata: in.Metadata,
		}
		reqs[i] = r
	}

	start := time.Now()
	policyDef := a.UniversalAPI.Resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore)
	bgrPolicyRunner := resiliency.NewRunner[[]state.BulkGetResponse](ctx, policyDef)
	responses, err := bgrPolicyRunner(func(ctx context.Context) ([]state.BulkGetResponse, error) {
		return store.BulkGet(ctx, reqs, state.BulkGetOpts{
			Parallelism: int(in.Parallelism),
		})
	})

	elapsed := diag.ElapsedSince(start)
	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.BulkGet, err == nil, elapsed)

	if err != nil {
		return bulkResp, err
	}

	bulkResp.Items = make([]*runtimev1pb.BulkStateItem, len(responses))
	for i := 0; i < len(responses); i++ {
		item := &runtimev1pb.BulkStateItem{
			Key:      stateLoader.GetOriginalStateKey(responses[i].Key),
			Data:     responses[i].Data,
			Etag:     stringValueOrEmpty(responses[i].ETag),
			Metadata: responses[i].Metadata,
			Error:    responses[i].Error,
		}
		bulkResp.Items[i] = item
	}

	if encryption.EncryptedStateStore(in.StoreName) {
		for i := range bulkResp.Items {
			if bulkResp.Items[i].Error != "" || len(bulkResp.Items[i].Data) == 0 {
				bulkResp.Items[i].Data = nil
				continue
			}

			val, err := encryption.TryDecryptValue(in.StoreName, bulkResp.Items[i].Data)
			if err != nil {
				apiServerLogger.Debugf("Bulk get error: %v", err)
				bulkResp.Items[i].Data = nil
				bulkResp.Items[i].Error = err.Error()
				continue
			}

			bulkResp.Items[i].Data = val
		}
	}

	return bulkResp, nil
}

func (a *api) GetState(ctx context.Context, in *runtimev1pb.GetStateRequest) (*runtimev1pb.GetStateResponse, error) {
	store, err := a.UniversalAPI.GetStateStore(in.StoreName)
	if err != nil {
		// Error has already been logged
		return &runtimev1pb.GetStateResponse{}, err
	}
	key, err := stateLoader.GetModifiedStateKey(in.Key, in.StoreName, a.UniversalAPI.AppID)
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
		a.UniversalAPI.Resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore),
	)
	getResponse, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
		return store.Get(ctx, req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.Get, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrStateGet, in.Key, in.StoreName, err.Error())
		a.UniversalAPI.Logger.Debug(err)
		return &runtimev1pb.GetStateResponse{}, err
	}

	if getResponse == nil {
		getResponse = &state.GetResponse{}
	}
	if encryption.EncryptedStateStore(in.StoreName) {
		val, err := encryption.TryDecryptValue(in.StoreName, getResponse.Data)
		if err != nil {
			err = status.Errorf(codes.Internal, messages.ErrStateGet, in.Key, in.StoreName, err.Error())
			a.UniversalAPI.Logger.Debug(err)
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

	store, err := a.UniversalAPI.GetStateStore(in.StoreName)
	if err != nil {
		// Error has already been logged
		return empty, err
	}

	l := len(in.States)
	if l == 0 {
		return empty, nil
	}

	reqs := make([]state.SetRequest, l)
	for i, s := range in.States {
		if len(s.Key) == 0 {
			return empty, status.Errorf(codes.InvalidArgument, "state key cannot be empty")
		}

		var key string
		key, err = stateLoader.GetModifiedStateKey(s.Key, in.StoreName, a.UniversalAPI.AppID)
		if err != nil {
			return empty, err
		}
		req := state.SetRequest{
			Key:      key,
			Metadata: s.Metadata,
		}

		if req.Metadata[contribMetadata.ContentType] == contenttype.JSONContentType {
			err = json.Unmarshal(s.Value, &req.Value)
			if err != nil {
				return empty, err
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
				a.UniversalAPI.Logger.Debug(encErr)
				return empty, encErr
			}

			req.Value = val
		}

		reqs[i] = req
	}

	start := time.Now()
	err = stateLoader.PerformBulkStoreOperation(ctx, reqs,
		a.UniversalAPI.Resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore),
		state.BulkStoreOpts{},
		store.Set,
		store.BulkSet,
	)
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.Set, err == nil, elapsed)

	if err != nil {
		err = a.stateErrorResponse(err, messages.ErrStateSave, in.StoreName, err.Error())
		a.UniversalAPI.Logger.Debug(err)
		return empty, err
	}
	return empty, nil
}

// stateErrorResponse takes a state store error, format and args and returns a status code encoded gRPC error.
func (a *api) stateErrorResponse(err error, format string, args ...interface{}) error {
	var etagErr *state.ETagError
	if errors.As(err, &etagErr) {
		switch etagErr.Kind() {
		case state.ETagMismatch:
			return status.Errorf(codes.Aborted, format, args...)
		case state.ETagInvalid:
			return status.Errorf(codes.InvalidArgument, format, args...)
		}
	}

	return status.Errorf(codes.Internal, format, args...)
}

func (a *api) DeleteState(ctx context.Context, in *runtimev1pb.DeleteStateRequest) (*emptypb.Empty, error) {
	empty := &emptypb.Empty{}

	store, err := a.UniversalAPI.GetStateStore(in.StoreName)
	if err != nil {
		// Error has already been logged
		return empty, err
	}

	key, err := stateLoader.GetModifiedStateKey(in.Key, in.StoreName, a.UniversalAPI.AppID)
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
		a.UniversalAPI.Resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.Delete(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.Delete, err == nil, elapsed)

	if err != nil {
		err = a.stateErrorResponse(err, messages.ErrStateDelete, in.Key, err.Error())
		a.UniversalAPI.Logger.Debug(err)
		return empty, err
	}
	return empty, nil
}

func (a *api) DeleteBulkState(ctx context.Context, in *runtimev1pb.DeleteBulkStateRequest) (*emptypb.Empty, error) {
	empty := &emptypb.Empty{}

	store, err := a.UniversalAPI.GetStateStore(in.StoreName)
	if err != nil {
		// Error has already been logged
		return empty, err
	}

	reqs := make([]state.DeleteRequest, len(in.States))
	for i, item := range in.States {
		key, err1 := stateLoader.GetModifiedStateKey(item.Key, in.StoreName, a.UniversalAPI.AppID)
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
		reqs[i] = req
	}

	start := time.Now()
	err = stateLoader.PerformBulkStoreOperation(ctx, reqs,
		a.UniversalAPI.Resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore),
		state.BulkStoreOpts{},
		store.Delete,
		store.BulkDelete,
	)
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, in.StoreName, diag.BulkDelete, err == nil, elapsed)

	if err != nil {
		err = a.stateErrorResponse(err, messages.ErrStateDeleteBulk, in.StoreName, err.Error())
		a.UniversalAPI.Logger.Debug(err)
		return empty, err
	}

	return empty, nil
}

func extractEtag(req *commonv1pb.StateItem) (bool, string) {
	if req.Etag != nil {
		return true, req.Etag.Value
	}
	return false, ""
}

func (a *api) ExecuteStateTransaction(ctx context.Context, in *runtimev1pb.ExecuteStateTransactionRequest) (*emptypb.Empty, error) {
	store, storeErr := a.UniversalAPI.GetStateStore(in.StoreName)
	if storeErr != nil {
		// Error has already been logged
		return &emptypb.Empty{}, storeErr
	}

	transactionalStore, ok := store.(state.TransactionalStore)
	if !ok {
		err := status.Errorf(codes.Unimplemented, messages.ErrStateStoreNotSupported, in.StoreName)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	operations := make([]state.TransactionalStateOperation, len(in.Operations))
	for i, inputReq := range in.Operations {
		req := inputReq.Request

		hasEtag, etag := extractEtag(req)
		key, err := stateLoader.GetModifiedStateKey(req.Key, in.StoreName, a.UniversalAPI.AppID)
		if err != nil {
			return &emptypb.Empty{}, err
		}
		switch state.OperationType(inputReq.OperationType) {
		case state.OperationUpsert:
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

			operations[i] = setReq

		case state.OperationDelete:
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

			operations[i] = delReq

		default:
			err := status.Errorf(codes.Unimplemented, messages.ErrNotSupportedStateOperation, inputReq.OperationType)
			apiServerLogger.Debug(err)
			return &emptypb.Empty{}, err
		}
	}

	if encryption.EncryptedStateStore(in.StoreName) {
		for i, op := range operations {
			switch req := op.(type) {
			case state.SetRequest:
				data := []byte(fmt.Sprintf("%v", req.Value))
				val, err := encryption.TryEncryptValue(in.StoreName, data)
				if err != nil {
					err = status.Errorf(codes.Internal, messages.ErrStateTransaction, err.Error())
					apiServerLogger.Debug(err)
					return &emptypb.Empty{}, err
				}

				req.Value = val
				operations[i] = req
			}
		}
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[struct{}](ctx,
		a.resiliency.ComponentOutboundPolicy(in.StoreName, resiliency.Statestore),
	)
	storeReq := &state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   in.Metadata,
	}
	_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
		return struct{}{}, transactionalStore.Multi(ctx, storeReq)
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
	if a.UniversalAPI.Actors == nil {
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
		j, err := json.Marshal(in.Data)
		if err != nil {
			return &emptypb.Empty{}, err
		}
		req.Data = j
	}
	err := a.UniversalAPI.Actors.CreateTimer(ctx, req)
	return &emptypb.Empty{}, err
}

func (a *api) UnregisterActorTimer(ctx context.Context, in *runtimev1pb.UnregisterActorTimerRequest) (*emptypb.Empty, error) {
	if a.UniversalAPI.Actors == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	req := &actors.DeleteTimerRequest{
		Name:      in.Name,
		ActorID:   in.ActorId,
		ActorType: in.ActorType,
	}

	err := a.UniversalAPI.Actors.DeleteTimer(ctx, req)
	return &emptypb.Empty{}, err
}

func (a *api) RegisterActorReminder(ctx context.Context, in *runtimev1pb.RegisterActorReminderRequest) (*emptypb.Empty, error) {
	if a.UniversalAPI.Actors == nil {
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
		j, err := json.Marshal(in.Data)
		if err != nil {
			return &emptypb.Empty{}, err
		}
		req.Data = j
	}
	err := a.UniversalAPI.Actors.CreateReminder(ctx, req)
	return &emptypb.Empty{}, err
}

func (a *api) UnregisterActorReminder(ctx context.Context, in *runtimev1pb.UnregisterActorReminderRequest) (*emptypb.Empty, error) {
	if a.UniversalAPI.Actors == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	req := &actors.DeleteReminderRequest{
		Name:      in.Name,
		ActorID:   in.ActorId,
		ActorType: in.ActorType,
	}

	err := a.UniversalAPI.Actors.DeleteReminder(ctx, req)
	return &emptypb.Empty{}, err
}

func (a *api) RenameActorReminder(ctx context.Context, in *runtimev1pb.RenameActorReminderRequest) (*emptypb.Empty, error) {
	if a.UniversalAPI.Actors == nil {
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

	err := a.UniversalAPI.Actors.RenameReminder(ctx, req)
	return &emptypb.Empty{}, err
}

func (a *api) GetActorState(ctx context.Context, in *runtimev1pb.GetActorStateRequest) (*runtimev1pb.GetActorStateResponse, error) {
	if a.UniversalAPI.Actors == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return nil, err
	}

	actorType := in.ActorType
	actorID := in.ActorId
	key := in.Key

	hosted := a.UniversalAPI.Actors.IsActorHosted(ctx, &actors.ActorHostedRequest{
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

	resp, err := a.UniversalAPI.Actors.GetState(ctx, &req)
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
	if a.UniversalAPI.Actors == nil {
		err := status.Errorf(codes.Internal, messages.ErrActorRuntimeNotFound)
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	actorType := in.ActorType
	actorID := in.ActorId
	actorOps := []actors.TransactionalOperation{}

	for _, op := range in.Operations {
		var actorOp actors.TransactionalOperation
		switch op.OperationType {
		case string(state.OperationUpsert):
			setReq := map[string]any{
				"key":   op.Key,
				"value": op.Value.Value,
				// Actor state do not user other attributes from state request.
			}
			if meta := op.GetMetadata(); len(meta) > 0 {
				setReq["metadata"] = meta
			}

			actorOp = actors.TransactionalOperation{
				Operation: actors.Upsert,
				Request:   setReq,
			}
		case string(state.OperationDelete):
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

	hosted := a.UniversalAPI.Actors.IsActorHosted(ctx, &actors.ActorHostedRequest{
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

	err := a.UniversalAPI.Actors.TransactionalStateOperation(ctx, &req)
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrActorStateTransactionSave, err))
		apiServerLogger.Debug(err)
		return &emptypb.Empty{}, err
	}

	return &emptypb.Empty{}, nil
}

func (a *api) InvokeActor(ctx context.Context, in *runtimev1pb.InvokeActorRequest) (*runtimev1pb.InvokeActorResponse, error) {
	response := &runtimev1pb.InvokeActorResponse{}

	if a.UniversalAPI.Actors == nil {
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
		WithMetadata(reqMetadata)
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
	policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef,
		resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
			Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
		},
	)
	resp, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return a.UniversalAPI.Actors.Call(ctx, req)
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

func (a *api) SetAppChannel(appChannel channel.AppChannel) {
	a.appChannel = appChannel
}

func (a *api) SetDirectMessaging(directMessaging messaging.DirectMessaging) {
	a.directMessaging = directMessaging
}

func (a *api) SetActorRuntime(actor actors.Actors) {
	a.UniversalAPI.Actors = actor
}

func stringValueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func (a *api) getConfigurationStore(name string) (configuration.Store, error) {
	if a.CompStore.ConfigurationsLen() == 0 {
		return nil, status.Error(codes.FailedPrecondition, messages.ErrConfigurationStoresNotConfigured)
	}

	conf, ok := a.CompStore.GetConfiguration(name)
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, messages.ErrConfigurationStoreNotFound, name)
	}
	return conf, nil
}

func (a *api) GetConfiguration(ctx context.Context, in *runtimev1pb.GetConfigurationRequest) (*runtimev1pb.GetConfigurationResponse, error) {
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

// TODO: Remove this method when the alpha API is removed.
func (a *api) GetConfigurationAlpha1(ctx context.Context, in *runtimev1pb.GetConfigurationRequest) (*runtimev1pb.GetConfigurationResponse, error) {
	return a.GetConfiguration(ctx, in)
}

type configurationEventHandler struct {
	readyCh      chan struct{}
	readyClosed  bool
	lock         sync.Mutex
	api          *api
	storeName    string
	serverStream runtimev1pb.Dapr_SubscribeConfigurationAlpha1Server //nolint:nosnakecase
}

func (h *configurationEventHandler) ready() {
	if !h.readyClosed {
		close(h.readyCh)
		h.readyClosed = true
	}
}

func (h *configurationEventHandler) updateEventHandler(ctx context.Context, e *configuration.UpdateEvent) error {
	// Blocks until the first message is sent
	<-h.readyCh

	// Calling Send on a gRPC stream from multiple goroutines at the same time is not supported
	h.lock.Lock()
	defer h.lock.Unlock()

	items := make(map[string]*commonv1pb.ConfigurationItem, len(e.Items))
	for k, v := range e.Items {
		items[k] = &commonv1pb.ConfigurationItem{
			Value:    v.Value,
			Version:  v.Version,
			Metadata: v.Metadata,
		}
	}

	err := h.serverStream.Send(&runtimev1pb.SubscribeConfigurationResponse{
		Items: items,
		Id:    e.ID,
	})
	if err != nil {
		apiServerLogger.Debug(err)
		return err
	}
	return nil
}

func (a *api) SubscribeConfiguration(request *runtimev1pb.SubscribeConfigurationRequest, configurationServer runtimev1pb.Dapr_SubscribeConfigurationServer) error { //nolint:nosnakecase
	store, err := a.getConfigurationStore(request.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return err
	}

	sort.Strings(request.Keys)

	// TODO(@halspang) provide a switch to use just resiliency or this.

	req := &configuration.SubscribeRequest{
		Keys:     request.Keys,
		Metadata: request.GetMetadata(),
	}

	handler := &configurationEventHandler{
		readyCh:      make(chan struct{}),
		api:          a,
		storeName:    request.StoreName,
		serverStream: configurationServer,
	}
	// Prevents a leak
	defer handler.ready()

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

	err = handler.serverStream.Send(&runtimev1pb.SubscribeConfigurationResponse{
		Id: subscribeID,
	})
	if err != nil {
		apiServerLogger.Debug(err)
		return err
	}

	stop := make(chan struct{})
	a.CompStore.AddConfigurationSubscribe(subscribeID, stop)

	// We have sent the first message, so signal that we're ready to send messages in the stream
	handler.ready()

	// Wait until the channel is stopped
	<-stop

	return nil
}

// TODO: Remove this method when the alpha API is removed.
func (a *api) SubscribeConfigurationAlpha1(request *runtimev1pb.SubscribeConfigurationRequest, configurationServer runtimev1pb.Dapr_SubscribeConfigurationAlpha1Server) error { //nolint:nosnakecase
	return a.SubscribeConfiguration(request, configurationServer.(runtimev1pb.Dapr_SubscribeConfigurationServer))
}

func (a *api) UnsubscribeConfiguration(ctx context.Context, request *runtimev1pb.UnsubscribeConfigurationRequest) (*runtimev1pb.UnsubscribeConfigurationResponse, error) {
	store, err := a.getConfigurationStore(request.GetStoreName())
	if err != nil {
		apiServerLogger.Debug(err)
		return &runtimev1pb.UnsubscribeConfigurationResponse{
			Ok:      false,
			Message: err.Error(),
		}, err
	}

	subscribeID := request.GetId()

	_, ok := a.CompStore.GetConfigurationSubscribe(subscribeID)
	if !ok {
		return &runtimev1pb.UnsubscribeConfigurationResponse{
			Ok:      false,
			Message: fmt.Sprintf(messages.ErrConfigurationUnsubscribe, subscribeID, "subscription does not exist"),
		}, nil
	}

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

	a.CompStore.DeleteConfigurationSubscribe(subscribeID)

	return &runtimev1pb.UnsubscribeConfigurationResponse{
		Ok: true,
	}, nil
}

// TODO: Remove this method when the alpha API is removed.
func (a *api) UnsubscribeConfigurationAlpha1(ctx context.Context, request *runtimev1pb.UnsubscribeConfigurationRequest) (*runtimev1pb.UnsubscribeConfigurationResponse, error) {
	return a.UnsubscribeConfiguration(ctx, request)
}

func (a *api) Close() error {
	a.CompStore.DeleteAllConfigurationSubscribe()
	return nil
}
