// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	auth "github.com/dapr/dapr/pkg/runtime/security"
)

// Channel is a concrete AppChannel implementation for interacting with gRPC based user code.
type Channel struct {
	client             *grpc.ClientConn
	baseAddress        string
	ch                 chan int
	tracingSpec        config.TracingSpec
	appMetadataToken   string
	maxRequestBodySize int
	readBufferSize     int
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
		c.ch = make(chan int, maxConcurrency)
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
	var rsp *invokev1.InvokeMethodResponse
	var err error

	switch req.APIVersion() {
	case internalv1pb.APIVersion_V1:
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
		g.ch <- 1
	}

	clientV1 := runtimev1pb.NewAppCallbackClient(g.client)
	grpcMetadata := invokev1.InternalMetadataToGrpcMetadata(ctx, req.Metadata(), true)

	if g.appMetadataToken != "" {
		grpcMetadata.Set(auth.APITokenHeader, g.appMetadataToken)
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
