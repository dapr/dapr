// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"fmt"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	clientv1pb "github.com/dapr/dapr/pkg/proto/daprclient/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Channel is a concrete AppChannel implementation for interacting with gRPC based user code
type Channel struct {
	client      *grpc.ClientConn
	baseAddress string
	ch          chan int
	tracingSpec config.TracingSpec
}

// CreateLocalChannel creates a gRPC connection with user code
func CreateLocalChannel(port, maxConcurrency int, conn *grpc.ClientConn, spec config.TracingSpec) *Channel {
	c := &Channel{
		client:      conn,
		baseAddress: fmt.Sprintf("%s:%d", channel.DefaultChannelAddress, port),
		tracingSpec: spec,
	}
	if maxConcurrency > 0 {
		c.ch = make(chan int, maxConcurrency)
	}
	return c
}

// GetBaseAddress returns the application base address
func (g *Channel) GetBaseAddress() string {
	return g.baseAddress
}

// InvokeMethod invokes user code via gRPC
func (g *Channel) InvokeMethod(req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	var rsp *invokev1.InvokeMethodResponse
	var err error

	if g.ch != nil {
		g.ch <- 1
	}

	switch req.APIVersion() {
	case commonv1pb.APIVersion_V1:
		rsp, err = g.invokeMethodV1(req)

	default:
		// Reject unsupported version
		rsp = nil
		err = status.Error(codes.Unimplemented, fmt.Sprintf("Unsupported spec version: %d", req.APIVersion()))
	}

	if g.ch != nil {
		<-g.ch
	}

	return rsp, err
}

// invokeMethodV1 calls user applications using daprclient v1
func (g *Channel) invokeMethodV1(req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	clientV1 := clientv1pb.NewDaprClientClient(g.client)

	ctx, cancel := context.WithTimeout(context.Background(), channel.DefaultChannelRequestTimeout)
	defer cancel()

	// Prepare gRPC Metadata
	ctx = metadata.NewOutgoingContext(ctx, invokev1.InternalMetadataToGrpcMetadata(*req.Metadata(), true))

	var header, trailer metadata.MD
	resp, err := clientV1.OnInvoke(ctx, req.Message(), grpc.Header(&header), grpc.Trailer(&trailer))

	// Convert status code
	respStatus := status.Convert(err)
	rsp := invokev1.NewInvokeMethodResponse(int32(respStatus.Code()), respStatus.Message(), &(respStatus.Proto().Details))
	rsp.WithHeaders(header).WithTrailers(trailer).WithInvokeResponseProto(resp)

	// Prepare response
	return rsp, nil
}
