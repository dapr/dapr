// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"fmt"
	"net"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	grpc_go "google.golang.org/grpc"
)

const appPort = 3000

// UserApp
type DaprServer interface {
}

func main() {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", appPort))
	grpcServer := grpc_go.NewServer()
	runtimev1pb.RegisterDaprServer(grpcServer, DaprServer{})
	if err := grpcServer.Serve(lis); err != nil {
		panic(err)
	}
}
