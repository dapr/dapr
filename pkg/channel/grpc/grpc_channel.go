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
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	auth "github.com/dapr/dapr/pkg/runtime/security"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
)

// Channel is a concrete AppChannel implementation for interacting with gRPC based user code.
type Channel struct {
	client             *grpc.ClientConn
	baseAddress        string
	ch                 chan struct{}
	tracingSpec        config.TracingSpec
	appMetadataToken   string
	maxRequestBodySize int
	readBufferSize     int
	appHealth          *apphealth.AppHealth
}

// CreateLocalChannel creates a gRPC connection with user code.
func CreateLocalChannel(port, maxConcurrency int, conn *grpc.ClientConn, spec config.TracingSpec, maxRequestBodySize int, readBufferSize int) *Channel {
	c := &Channel{
		client:             conn,
		baseAddress:        fmt.Sprintf("%s:%d", channel.DefaultChannelAddress, port),
		tracingSpec:        spec,
		appMetadataToken:   auth.GetAppToken(),
		maxRequestBodySize: maxRequestBodySize,
		readBufferSize:     readBufferSize,
	}
	if maxConcurrency > 0 {
		c.ch = make(chan struct{}, maxConcurrency)
	}
	return c
}

// GetBaseAddress returns the application base address.
func (g *Channel) GetBaseAddress() string {
	return g.baseAddress
}

// GetAppConfig gets application config from user application.
func (g *Channel) GetAppConfig() (*config.ApplicationConfig, error) {
	return nil, nil
}

// InvokeMethod invokes user code via gRPC.
func (g *Channel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	if g.appHealth != nil && g.appHealth.GetStatus() != apphealth.AppStatusHealthy {
		return nil, status.Error(codes.Internal, messages.ErrAppUnhealthy)
	}

	var rsp *invokev1.InvokeMethodResponse
	var err error

	switch req.APIVersion() {
	case internalv1pb.APIVersion_V1: //nolint:nosnakecase
		rsp, err = g.invokeMethodV1(ctx, req)

	default:
		// Reject unsupported version
		rsp = nil
		err = status.Error(codes.Unimplemented, fmt.Sprintf("Unsupported spec version: %d", req.APIVersion()))
	}

	return rsp, err
}

// invokeMethodV1 calls user applications using daprclient v1.
func (g *Channel) invokeMethodV1(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	if g.ch != nil {
		g.ch <- struct{}{}
	}

	clientV1 := runtimev1pb.NewAppCallbackClient(g.client)
	grpcMetadata := invokev1.InternalMetadataToGrpcMetadata(ctx, req.Metadata(), true)

	if g.appMetadataToken != "" {
		grpcMetadata.Set(authConsts.APITokenHeader, g.appMetadataToken)
	}

	// Prepare gRPC Metadata
	ctx = metadata.NewOutgoingContext(context.Background(), grpcMetadata)

	var header, trailer metadata.MD

	var opts []grpc.CallOption
	opts = append(opts, grpc.Header(&header), grpc.Trailer(&trailer),
		grpc.MaxCallSendMsgSize(g.maxRequestBodySize*1024*1024), grpc.MaxCallRecvMsgSize(g.maxRequestBodySize*1024*1024))

	resp, err := clientV1.OnInvoke(ctx, req.Message(), opts...)

	if g.ch != nil {
		<-g.ch
	}

	var rsp *invokev1.InvokeMethodResponse
	if err != nil {
		// Convert status code
		respStatus := status.Convert(err)
		// Prepare response
		rsp = invokev1.NewInvokeMethodResponse(int32(respStatus.Code()), respStatus.Message(), respStatus.Proto().Details)
	} else {
		rsp = invokev1.NewInvokeMethodResponse(int32(codes.OK), "", nil)
	}

	rsp.WithHeaders(header).WithTrailers(trailer)

	return rsp.WithMessage(resp), nil
}

// HealthProbe performs a health probe.
func (g *Channel) HealthProbe(ctx context.Context) (bool, error) {
	clientV1 := runtimev1pb.NewAppCallbackHealthCheckClient(g.client)
	_, err := clientV1.HealthCheck(ctx, &emptypb.Empty{})

	// Errors here are network-level errors, so we are not returning them as errors
	// Instead, we just return a failed probe
	return err == nil, nil
}

// SetAppHealth sets the apphealth.AppHealth object.
func (g *Channel) SetAppHealth(ah *apphealth.AppHealth) {
	g.appHealth = ah
}
