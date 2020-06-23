// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"context"
	"fmt"
	"net"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/golang/protobuf/ptypes/empty"
	grpc_go "google.golang.org/grpc"
)

const appPort = 3000

// DaprServer is a barebones application
type DaprServer struct {
}

func (s *DaprServer) CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	var resp = invokev1.NewInvokeMethodResponse(0, "", nil)
	return resp.Proto(), nil
}

func (s *DaprServer) CallActor(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	var resp = invokev1.NewInvokeMethodResponse(0, "", nil)
	return resp.Proto(), nil
}

func (s *DaprServer) PublishEvent(ctx context.Context, in *runtimev1pb.PublishEventRequest) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (s *DaprServer) InvokeService(ctx context.Context, in *runtimev1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error) {
	return &commonv1pb.InvokeResponse{}, nil
}

func (s *DaprServer) InvokeBinding(ctx context.Context, in *runtimev1pb.InvokeBindingRequest) (*runtimev1pb.InvokeBindingResponse, error) {
	return &runtimev1pb.InvokeBindingResponse{}, nil
}

func (s *DaprServer) GetState(ctx context.Context, in *runtimev1pb.GetStateRequest) (*runtimev1pb.GetStateResponse, error) {
	return &runtimev1pb.GetStateResponse{}, nil
}

func (s *DaprServer) SaveState(ctx context.Context, in *runtimev1pb.SaveStateRequest) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (s *DaprServer) DeleteState(ctx context.Context, in *runtimev1pb.DeleteStateRequest) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (s *DaprServer) GetSecret(ctx context.Context, in *runtimev1pb.GetSecretRequest) (*runtimev1pb.GetSecretResponse, error) {
	return &runtimev1pb.GetSecretResponse{}, nil
}

func (s *DaprServer) ExecuteStateTransaction(ctx context.Context, in *runtimev1pb.ExecuteStateTransactionRequest) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func main() {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", appPort))
	grpcServer := grpc_go.NewServer()
	runtimev1pb.RegisterDaprServer(grpcServer, &DaprServer{})
	if err := grpcServer.Serve(lis); err != nil {
		panic(err)
	}
}
