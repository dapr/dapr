// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/channel"
	tracing "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	daprv1pb "github.com/dapr/dapr/pkg/proto/dapr/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/daprinternal/v1"
	"github.com/golang/protobuf/ptypes/any"
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
)

// API is the gRPC interface for the Dapr gRPC API. It implements both the internal and external proto definitions.
type API interface {
	// DaprInternal Service methods
	CallActor(ctx context.Context, in *internalv1pb.CallActorRequest) (*internalv1pb.CallActorResponse, error)
	CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)

	// Dapr Service methods
	PublishEvent(ctx context.Context, in *daprv1pb.PublishEventEnvelope) (*empty.Empty, error)
	InvokeService(ctx context.Context, in *daprv1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error)
	InvokeBinding(ctx context.Context, in *daprv1pb.InvokeBindingEnvelope) (*empty.Empty, error)
	GetState(ctx context.Context, in *daprv1pb.GetStateEnvelope) (*daprv1pb.GetStateResponseEnvelope, error)
	GetSecret(ctx context.Context, in *daprv1pb.GetSecretEnvelope) (*daprv1pb.GetSecretResponseEnvelope, error)
	SaveState(ctx context.Context, in *daprv1pb.SaveStateEnvelope) (*empty.Empty, error)
	DeleteState(ctx context.Context, in *daprv1pb.DeleteStateEnvelope) (*empty.Empty, error)
}

type api struct {
	actor                 actors.Actors
	directMessaging       messaging.DirectMessaging
	appChannel            channel.AppChannel
	stateStores           map[string]state.Store
	secretStores          map[string]secretstores.SecretStore
	publishFn             func(req *pubsub.PublishRequest) error
	id                    string
	sendToOutputBindingFn func(name string, req *bindings.WriteRequest) error
}

// NewAPI returns a new gRPC API
func NewAPI(
	appID string, appChannel channel.AppChannel,
	stateStores map[string]state.Store,
	secretStores map[string]secretstores.SecretStore,
	publishFn func(req *pubsub.PublishRequest) error,
	directMessaging messaging.DirectMessaging,
	actor actors.Actors,
	sendToOutputBindingFn func(name string, req *bindings.WriteRequest) error) API {
	return &api{
		directMessaging:       directMessaging,
		actor:                 actor,
		id:                    appID,
		appChannel:            appChannel,
		publishFn:             publishFn,
		stateStores:           stateStores,
		secretStores:          secretStores,
		sendToOutputBindingFn: sendToOutputBindingFn,
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
	resp, err := a.appChannel.InvokeMethod(req)
	return resp.Proto(), err
}

// CallActor invokes a virtual actor
func (a *api) CallActor(ctx context.Context, in *internalv1pb.CallActorRequest) (*internalv1pb.CallActorResponse, error) {
	req := actors.CallRequest{
		ActorType: in.ActorType,
		ActorID:   in.ActorId,
		Data:      in.Data.Value,
		Method:    in.Method,
		Metadata:  in.Metadata,
	}

	resp, err := a.actor.Call(&req)
	if err != nil {
		return nil, err
	}

	return &internalv1pb.CallActorResponse{
		Data:     &any.Any{Value: resp.Data},
		Metadata: map[string]string{},
	}, nil
}

func (a *api) PublishEvent(ctx context.Context, in *daprv1pb.PublishEventEnvelope) (*empty.Empty, error) {
	if a.publishFn == nil {
		return &empty.Empty{}, errors.New("ERR_PUBSUB_NOT_FOUND")
	}

	topic := in.Topic
	body := []byte{}

	if in.Data != nil {
		body = in.Data.Value
	}

	corID, ok := ctx.Value(tracing.CorrelationID).(string)
	if !ok {
		corID = ""
	}

	envelope := pubsub.NewCloudEventsEnvelope(uuid.New().String(), a.id, pubsub.DefaultCloudEventType, corID, body)
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

func (a *api) InvokeService(ctx context.Context, in *daprv1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error) {
	req := invokev1.FromInvokeRequestMessage(in.GetMessage())

	if incomingMD, ok := metadata.FromIncomingContext(ctx); ok {
		req.WithMetadata(incomingMD)
	}

	resp, err := a.directMessaging.Invoke(in.Id, req)
	if err != nil {
		return nil, err
	}

	// TODO: Add trace headers
	grpc.SendHeader(ctx, invokev1.InternalMetadataToGrpcMetadata(resp.Headers(), true))

	statusCode := int(resp.Status().Code)
	if resp.IsHTTPResponse() {
		statusCode = int(invokev1.CodeFromHTTPStatus(statusCode))
	} else {
		// ignore if appchannel uses HTTP
		grpc.SetTrailer(ctx, invokev1.InternalMetadataToGrpcMetadata(resp.Trailers(), false))
	}

	return resp.Message(), status.Error(codes.Code(statusCode), resp.Status().Message)
}

func (a *api) InvokeBinding(ctx context.Context, in *daprv1pb.InvokeBindingEnvelope) (*empty.Empty, error) {
	req := &bindings.WriteRequest{
		Metadata: in.Metadata,
	}
	if in.Data != nil {
		req.Data = in.Data.Value
	}

	err := a.sendToOutputBindingFn(in.Name, req)
	if err != nil {
		return &empty.Empty{}, fmt.Errorf("ERR_INVOKE_OUTPUT_BINDING: %s", err)
	}
	return &empty.Empty{}, nil
}

func (a *api) GetState(ctx context.Context, in *daprv1pb.GetStateEnvelope) (*daprv1pb.GetStateResponseEnvelope, error) {
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
			Consistency: in.Consistency,
		},
	}

	getResponse, err := a.stateStores[storeName].Get(&req)
	if err != nil {
		return nil, fmt.Errorf("ERR_STATE_GET: %s", err)
	}

	response := &daprv1pb.GetStateResponseEnvelope{}
	if getResponse != nil {
		response.Etag = getResponse.ETag
		response.Data = &any.Any{Value: getResponse.Data}
	}
	return response, nil
}

func (a *api) SaveState(ctx context.Context, in *daprv1pb.SaveStateEnvelope) (*empty.Empty, error) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		return &empty.Empty{}, errors.New("ERR_STATE_STORE_NOT_CONFIGURED")
	}

	storeName := in.StoreName

	if a.stateStores[storeName] == nil {
		return &empty.Empty{}, errors.New("ERR_STATE_STORE_NOT_FOUND")
	}

	reqs := []state.SetRequest{}
	for _, s := range in.Requests {
		req := state.SetRequest{
			Key:      a.getModifiedStateKey(s.Key),
			Metadata: s.Metadata,
			Value:    s.Value.Value,
			ETag:     s.Etag,
		}
		if s.Options != nil {
			req.Options = state.SetStateOption{
				Consistency: s.Options.Consistency,
				Concurrency: s.Options.Concurrency,
			}
			if s.Options.RetryPolicy != nil {
				req.Options.RetryPolicy = state.RetryPolicy{
					Threshold: int(s.Options.RetryPolicy.Threshold),
					Pattern:   s.Options.RetryPolicy.Pattern,
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

func (a *api) DeleteState(ctx context.Context, in *daprv1pb.DeleteStateEnvelope) (*empty.Empty, error) {
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
			Concurrency: in.Options.Concurrency,
			Consistency: in.Options.Consistency,
		}

		if in.Options.RetryPolicy != nil {
			retryPolicy := state.RetryPolicy{
				Threshold: int(in.Options.RetryPolicy.Threshold),
				Pattern:   in.Options.RetryPolicy.Pattern,
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

func (a *api) GetSecret(ctx context.Context, in *daprv1pb.GetSecretEnvelope) (*daprv1pb.GetSecretResponseEnvelope, error) {
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

	response := &daprv1pb.GetSecretResponseEnvelope{}
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
