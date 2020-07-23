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
	"time"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	durpb "github.com/golang/protobuf/ptypes/duration"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/google/uuid"
	jsoniter "github.com/json-iterator/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	// Range of a durpb.Duration in seconds, as specified in
	// google/protobuf/duration.proto. This is about 10,000 years in seconds.
	maxSeconds    = int64(10000 * 365.25 * 24 * 60 * 60)
	minSeconds    = -maxSeconds
	daprSeparator = "||"

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
	GetSecret(ctx context.Context, in *runtimev1pb.GetSecretRequest) (*runtimev1pb.GetSecretResponse, error)
	SaveState(ctx context.Context, in *runtimev1pb.SaveStateRequest) (*empty.Empty, error)
	DeleteState(ctx context.Context, in *runtimev1pb.DeleteStateRequest) (*empty.Empty, error)
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
		return &empty.Empty{}, errors.New("ERR_PUBSUB_NOT_FOUND")
	}

	topic := in.Topic
	body := []byte{}

	if in.Data != nil {
		body = in.Data
	}

	span := diag_utils.SpanFromContext(ctx)
	corID := diag.SpanContextToW3CString(span.SpanContext())
	envelope := pubsub.NewCloudEventsEnvelope(uuid.New().String(), a.id, pubsub.DefaultCloudEventType, corID, topic, body)
	b, err := jsoniter.ConfigFastest.Marshal(envelope)
	if err != nil {
		return &empty.Empty{}, fmt.Errorf("ERR_PUBSUB_CLOUD_EVENTS_SER: %s", err)
	}

	req := pubsub.PublishRequest{
		Topic: topic,
		Data:  b,
	}

	err = a.publishFn(&req)
	if err != nil {
		return &empty.Empty{}, fmt.Errorf("ERR_PUBSUB_PUBLISH_MESSAGE: %s", err)
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
		return r, fmt.Errorf("ERR_INVOKE_OUTPUT_BINDING: %s", err)
	}

	if resp != nil {
		r.Data = resp.Data
		r.Metadata = resp.Metadata
	}
	return r, nil
}

func (a *api) GetState(ctx context.Context, in *runtimev1pb.GetStateRequest) (*runtimev1pb.GetStateResponse, error) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		return nil, errors.New("ERR_STATE_STORE_NOT_CONFIGURED")
	}

	storeName := in.StoreName

	if a.stateStores[storeName] == nil {
		return nil, errors.New("ERR_STATE_STORE_NOT_FOUND")
	}

	req := state.GetRequest{
		Key: a.getModifiedStateKey(in.Key),
		Options: state.GetStateOption{
			Consistency: stateConsistencyToString(in.Consistency),
		},
	}

	getResponse, err := a.stateStores[storeName].Get(&req)
	if err != nil {
		return nil, fmt.Errorf("ERR_STATE_GET: %s", err)
	}

	response := &runtimev1pb.GetStateResponse{}
	if getResponse != nil {
		response.Etag = getResponse.ETag
		response.Data = getResponse.Data
	}
	return response, nil
}

func (a *api) SaveState(ctx context.Context, in *runtimev1pb.SaveStateRequest) (*empty.Empty, error) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		return &empty.Empty{}, errors.New("ERR_STATE_STORE_NOT_CONFIGURED")
	}

	storeName := in.StoreName

	if a.stateStores[storeName] == nil {
		return &empty.Empty{}, errors.New("ERR_STATE_STORE_NOT_FOUND")
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
			if s.Options.RetryPolicy != nil {
				req.Options.RetryPolicy = state.RetryPolicy{
					Threshold: int(s.Options.RetryPolicy.Threshold),
					Pattern:   retryPatternToString(s.Options.RetryPolicy.Pattern),
				}
				if s.Options.RetryPolicy.Interval != nil {
					dur, err := duration(s.Options.RetryPolicy.Interval)
					if err == nil {
						req.Options.RetryPolicy.Interval = dur
					}
				}
			}
		}
		reqs = append(reqs, req)
	}

	err := a.stateStores[storeName].BulkSet(reqs)
	if err != nil {
		return &empty.Empty{}, fmt.Errorf("ERR_STATE_SAVE: %s", err)
	}
	return &empty.Empty{}, nil
}

func (a *api) DeleteState(ctx context.Context, in *runtimev1pb.DeleteStateRequest) (*empty.Empty, error) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		return &empty.Empty{}, errors.New("ERR_STATE_STORE_NOT_CONFIGURED")
	}

	storeName := in.StoreName

	if a.stateStores[storeName] == nil {
		return &empty.Empty{}, errors.New("ERR_STATE_STORE_NOT_FOUND")
	}

	req := state.DeleteRequest{
		Key:  a.getModifiedStateKey(in.Key),
		ETag: in.Etag,
	}
	if in.Options != nil {
		req.Options = state.DeleteStateOption{
			Concurrency: stateConcurrencyToString(in.Options.Concurrency),
			Consistency: stateConsistencyToString(in.Options.Consistency),
		}

		if in.Options.RetryPolicy != nil {
			retryPolicy := state.RetryPolicy{
				Threshold: int(in.Options.RetryPolicy.Threshold),
				Pattern:   retryPatternToString(in.Options.RetryPolicy.Pattern),
			}
			if in.Options.RetryPolicy.Interval != nil {
				dur, err := duration(in.Options.RetryPolicy.Interval)
				if err == nil {
					retryPolicy.Interval = dur
				}
			}
			req.Options.RetryPolicy = retryPolicy
		}
	}

	err := a.stateStores[storeName].Delete(&req)
	if err != nil {
		return &empty.Empty{}, fmt.Errorf("ERR_STATE_DELETE: failed deleting state with key %s: %s", in.Key, err)
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
		return nil, errors.New("ERR_SECRET_STORE_NOT_CONFIGURED")
	}

	secretStoreName := in.StoreName

	if a.secretStores[secretStoreName] == nil {
		return nil, errors.New("ERR_SECRET_STORE_NOT_FOUND")
	}

	req := secretstores.GetSecretRequest{
		Name:     in.Key,
		Metadata: in.Metadata,
	}

	getResponse, err := a.secretStores[secretStoreName].GetSecret(req)

	if err != nil {
		return nil, fmt.Errorf("ERR_SECRET_GET: %s", err)
	}

	response := &runtimev1pb.GetSecretResponse{}
	if getResponse.Data != nil {
		response.Data = getResponse.Data
	}
	return response, nil
}

func duration(p *durpb.Duration) (time.Duration, error) {
	if err := validateDuration(p); err != nil {
		return 0, err
	}
	d := time.Duration(p.Seconds) * time.Second
	if int64(d/time.Second) != p.Seconds {
		return 0, fmt.Errorf("duration: %v is out of range for time.Duration", p)
	}
	if p.Nanos != 0 {
		d += time.Duration(p.Nanos) * time.Nanosecond
		if (d < 0) != (p.Nanos < 0) {
			return 0, fmt.Errorf("duration: %v is out of range for time.Duration", p)
		}
	}
	return d, nil
}

func validateDuration(d *durpb.Duration) error {
	if d == nil {
		return errors.New("duration: nil Duration")
	}
	if d.Seconds < minSeconds || d.Seconds > maxSeconds {
		return fmt.Errorf("duration: %v: seconds out of range", d)
	}
	if d.Nanos <= -1e9 || d.Nanos >= 1e9 {
		return fmt.Errorf("duration: %v: nanos out of range", d)
	}
	// Seconds and Nanos must have the same sign, unless d.Nanos is zero.
	if (d.Seconds < 0 && d.Nanos > 0) || (d.Seconds > 0 && d.Nanos < 0) {
		return fmt.Errorf("duration: %v: seconds and nanos have different signs", d)
	}
	return nil
}
