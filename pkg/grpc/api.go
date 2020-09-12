// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/google/uuid"
	jsoniter "github.com/json-iterator/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	daprSeparator        = "||"
	daprHTTPStatusHeader = "dapr-http-status"
)

// API is the gRPC interface for the Dapr gRPC API. It implements both the internal and external proto definitions.
type API interface {
	// DaprInternal Service methods
	CallActor(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)

	// Dapr Service methods
	PublishEvent(ctx context.Context, in *runtimev1pb.PublishEventRequest) (*empty.Empty, error)
	InvokeService(ctx context.Context, in *runtimev1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error)
	InvokeBinding(ctx context.Context, in *runtimev1pb.InvokeBindingRequest) (*runtimev1pb.InvokeBindingResponse, error)
	GetState(ctx context.Context, in *runtimev1pb.GetStateRequest) (*runtimev1pb.GetStateResponse, error)
	GetBulkState(ctx context.Context, in *runtimev1pb.GetBulkStateRequest) (*runtimev1pb.GetBulkStateResponse, error)
	GetSecret(ctx context.Context, in *runtimev1pb.GetSecretRequest) (*runtimev1pb.GetSecretResponse, error)
	SaveState(ctx context.Context, in *runtimev1pb.SaveStateRequest) (*empty.Empty, error)
	DeleteState(ctx context.Context, in *runtimev1pb.DeleteStateRequest) (*empty.Empty, error)
	ExecuteStateTransaction(ctx context.Context, in *runtimev1pb.ExecuteStateTransactionRequest) (*empty.Empty, error)
}

type api struct {
	actor                 actors.Actors
	directMessaging       messaging.DirectMessaging
	appChannel            channel.AppChannel
	stateStores           map[string]state.Store
	secretStores          map[string]secretstores.SecretStore
	publishFn             func(req *pubsub.PublishRequest) error
	id                    string
	sendToOutputBindingFn func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	tracingSpec           config.TracingSpec
}

// NewAPI returns a new gRPC API
func NewAPI(
	appID string, appChannel channel.AppChannel,
	stateStores map[string]state.Store,
	secretStores map[string]secretstores.SecretStore,
	publishFn func(req *pubsub.PublishRequest) error,
	directMessaging messaging.DirectMessaging,
	actor actors.Actors,
	sendToOutputBindingFn func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error),
	tracingSpec config.TracingSpec) API {
	return &api{
		directMessaging:       directMessaging,
		actor:                 actor,
		id:                    appID,
		appChannel:            appChannel,
		publishFn:             publishFn,
		stateStores:           stateStores,
		secretStores:          secretStores,
		sendToOutputBindingFn: sendToOutputBindingFn,
		tracingSpec:           tracingSpec,
	}
}

// CallLocal is used for internal dapr to dapr calls. It is invoked by another Dapr instance with a request to the local app.
func (a *api) CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	if a.appChannel == nil {
		return nil, status.Error(codes.Internal, "app channel is not initialized")
	}

	req, err := invokev1.InternalInvokeRequest(in)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parsing InternalInvokeRequest error: %s", err.Error())
	}

	resp, err := a.appChannel.InvokeMethod(ctx, req)

	if err != nil {
		return nil, err
	}
	return resp.Proto(), err
}

// CallActor invokes a virtual actor
func (a *api) CallActor(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	req, err := invokev1.InternalInvokeRequest(in)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parsing InternalInvokeRequest error: %s", err.Error())
	}

	resp, err := a.actor.Call(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Proto(), nil
}

func (a *api) PublishEvent(ctx context.Context, in *runtimev1pb.PublishEventRequest) (*empty.Empty, error) {
	if a.publishFn == nil {
		err := errors.New("ERR_PUBSUB_NOT_FOUND")
		apiServerLogger.Debug(err)
		return &empty.Empty{}, err
	}

	topic := in.Topic
	body := []byte{}

	if in.Data != nil {
		body = in.Data
	}
	pubsubName := in.PubsubName
	span := diag_utils.SpanFromContext(ctx)
	corID := diag.SpanContextToW3CString(span.SpanContext())
	envelope := pubsub.NewCloudEventsEnvelope(uuid.New().String(), a.id, pubsub.DefaultCloudEventType, corID, topic, pubsubName, body)
	b, err := jsoniter.ConfigFastest.Marshal(envelope)
	if err != nil {
		err = fmt.Errorf("ERR_PUBSUB_CLOUD_EVENTS_SER: %s", err)
		apiServerLogger.Debug(err)
		return &empty.Empty{}, err
	}

	req := pubsub.PublishRequest{
		PubsubName: pubsubName,
		Topic:      topic,
		Data:       b,
	}

	err = a.publishFn(&req)
	if err != nil {
		err = fmt.Errorf("ERR_PUBSUB_PUBLISH_MESSAGE: %s", err)
		apiServerLogger.Debug(err)
		return &empty.Empty{}, err
	}
	return &empty.Empty{}, nil
}

func (a *api) InvokeService(ctx context.Context, in *runtimev1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error) {
	req := invokev1.FromInvokeRequestMessage(in.GetMessage())

	if incomingMD, ok := metadata.FromIncomingContext(ctx); ok {
		req.WithMetadata(incomingMD)
	}

	resp, err := a.directMessaging.Invoke(ctx, in.Id, req)
	if err != nil {
		return nil, err
	}

	var headerMD = invokev1.InternalMetadataToGrpcMetadata(ctx, resp.Headers(), true)

	var respError error
	if resp.IsHTTPResponse() {
		var errorMessage = []byte("")
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

	return resp.Message(), respError
}

func (a *api) InvokeBinding(ctx context.Context, in *runtimev1pb.InvokeBindingRequest) (*runtimev1pb.InvokeBindingResponse, error) {
	req := &bindings.InvokeRequest{
		Metadata:  in.Metadata,
		Operation: bindings.OperationKind(in.Operation),
	}
	if in.Data != nil {
		req.Data = in.Data
	}

	r := &runtimev1pb.InvokeBindingResponse{}
	resp, err := a.sendToOutputBindingFn(in.Name, req)
	if err != nil {
		err = fmt.Errorf("ERR_INVOKE_OUTPUT_BINDING: %s", err)
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

	resp := &runtimev1pb.GetBulkStateResponse{}
	limiter := concurrency.NewLimiter(int(in.Parallelism))

	for _, k := range in.Keys {
		fn := func(param interface{}) {
			req := state.GetRequest{
				Key:      a.getModifiedStateKey(param.(string)),
				Metadata: in.Metadata,
			}

			r, err := store.Get(&req)
			item := &runtimev1pb.BulkStateItem{
				Key: param.(string),
			}
			if err != nil {
				item.Error = err.Error()
			} else if r != nil {
				item.Data = r.Data
				item.Etag = r.ETag
			}
			resp.Items = append(resp.Items, item)
		}

		limiter.Execute(fn, k)
	}
	limiter.Wait()

	return resp, nil
}

func (a *api) getStateStore(name string) (state.Store, error) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		return nil, errors.New("ERR_STATE_STORE_NOT_CONFIGURED")
	}

	if a.stateStores[name] == nil {
		return nil, errors.New("ERR_STATE_STORE_NOT_FOUND")
	}
	return a.stateStores[name], nil
}

func (a *api) GetState(ctx context.Context, in *runtimev1pb.GetStateRequest) (*runtimev1pb.GetStateResponse, error) {
	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetStateResponse{}, err
	}

	req := state.GetRequest{
		Key:      a.getModifiedStateKey(in.Key),
		Metadata: in.Metadata,
		Options: state.GetStateOption{
			Consistency: stateConsistencyToString(in.Consistency),
		},
	}

	getResponse, err := store.Get(&req)
	if err != nil {
		err = fmt.Errorf("ERR_STATE_GET: %s", err)
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetStateResponse{}, err
	}

	response := &runtimev1pb.GetStateResponse{}
	if getResponse != nil {
		response.Etag = getResponse.ETag
		response.Data = getResponse.Data
	}
	return response, nil
}

func (a *api) SaveState(ctx context.Context, in *runtimev1pb.SaveStateRequest) (*empty.Empty, error) {
	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return &empty.Empty{}, err
	}

	reqs := []state.SetRequest{}
	for _, s := range in.States {
		req := state.SetRequest{
			Key:      a.getModifiedStateKey(s.Key),
			Metadata: s.Metadata,
			Value:    s.Value,
			ETag:     s.Etag,
		}
		if s.Options != nil {
			req.Options = state.SetStateOption{
				Consistency: stateConsistencyToString(s.Options.Consistency),
				Concurrency: stateConcurrencyToString(s.Options.Concurrency),
			}
		}
		reqs = append(reqs, req)
	}

	err = store.BulkSet(reqs)
	if err != nil {
		err = fmt.Errorf("ERR_STATE_SAVE: %s", err)
		apiServerLogger.Debug(err)
		return &empty.Empty{}, err
	}
	return &empty.Empty{}, nil
}

func (a *api) DeleteState(ctx context.Context, in *runtimev1pb.DeleteStateRequest) (*empty.Empty, error) {
	store, err := a.getStateStore(in.StoreName)
	if err != nil {
		apiServerLogger.Debug(err)
		return &empty.Empty{}, err
	}

	req := state.DeleteRequest{
		Key:      a.getModifiedStateKey(in.Key),
		Metadata: in.Metadata,
		ETag:     in.Etag,
	}
	if in.Options != nil {
		req.Options = state.DeleteStateOption{
			Concurrency: stateConcurrencyToString(in.Options.Concurrency),
			Consistency: stateConsistencyToString(in.Options.Consistency),
		}
	}

	err = store.Delete(&req)
	if err != nil {
		err = fmt.Errorf("ERR_STATE_DELETE: failed deleting state with key %s: %s", in.Key, err)
		apiServerLogger.Debug(err)
		return &empty.Empty{}, err
	}
	return &empty.Empty{}, nil
}

func (a *api) getModifiedStateKey(key string) string {
	if a.id != "" {
		return fmt.Sprintf("%s%s%s", a.id, daprSeparator, key)
	}
	return key
}

func (a *api) GetSecret(ctx context.Context, in *runtimev1pb.GetSecretRequest) (*runtimev1pb.GetSecretResponse, error) {
	if a.secretStores == nil || len(a.secretStores) == 0 {
		err := errors.New("ERR_SECRET_STORE_NOT_CONFIGURED")
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetSecretResponse{}, err
	}

	secretStoreName := in.StoreName

	if a.secretStores[secretStoreName] == nil {
		err := errors.New("ERR_SECRET_STORE_NOT_FOUND")
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetSecretResponse{}, err
	}

	req := secretstores.GetSecretRequest{
		Name:     in.Key,
		Metadata: in.Metadata,
	}

	getResponse, err := a.secretStores[secretStoreName].GetSecret(req)

	if err != nil {
		err = fmt.Errorf("ERR_SECRET_GET: %s", err)
		apiServerLogger.Debug(err)
		return &runtimev1pb.GetSecretResponse{}, err
	}

	response := &runtimev1pb.GetSecretResponse{}
	if getResponse.Data != nil {
		response.Data = getResponse.Data
	}
	return response, nil
}

func (a *api) ExecuteStateTransaction(ctx context.Context, in *runtimev1pb.ExecuteStateTransactionRequest) (*empty.Empty, error) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		err := errors.New("ERR_STATE_STORE_NOT_CONFIGURED")
		apiServerLogger.Debug(err)
		return &empty.Empty{}, err
	}

	storeName := in.StoreName

	if a.stateStores[storeName] == nil {
		err := errors.New("ERR_STATE_STORE_NOT_FOUND")
		apiServerLogger.Debug(err)
		return &empty.Empty{}, err
	}

	transactionalStore, ok := a.stateStores[storeName].(state.TransactionalStore)
	if !ok {
		err := errors.New("ERR_STATE_STORE_NOT_SUPPORTED")
		apiServerLogger.Debug(err)
		return &empty.Empty{}, err
	}

	operations := []state.TransactionalStateOperation{}
	for _, inputReq := range in.Operations {
		var operation state.TransactionalStateOperation
		var req = inputReq.Request
		switch state.OperationType(inputReq.OperationType) {
		case state.Upsert:
			setReq := state.SetRequest{
				Key: a.getModifiedStateKey(req.Key),
				// Limitation:
				// type conversion is required because Multi of some statestore
				// implementation cannot handle []byte properly.
				Value:    string(req.Value),
				Metadata: req.Metadata,
				ETag:     req.Etag,
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
				Key:      a.getModifiedStateKey(req.Key),
				Metadata: req.Metadata,
				ETag:     req.Etag,
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
			err := fmt.Errorf("ERR_OPERATION_NOT_SUPPORTED: operation type %s not supported", inputReq.OperationType)
			apiServerLogger.Debug(err)
			return &empty.Empty{}, err
		}

		operations = append(operations, operation)
	}

	err := transactionalStore.Multi(&state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   in.Metadata,
	})

	if err != nil {
		err = fmt.Errorf("ERR_STATE_TRANSACTION: %s", err)
		apiServerLogger.Debug(err)
		return &empty.Empty{}, err
	}
	return &empty.Empty{}, nil
}
