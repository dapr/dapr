package grpc

import (
	"context"
	grpcGo "google.golang.org/grpc"
)

type Middleware = func(ctx context.Context, req any, _ *grpcGo.UnaryServerInfo, handler grpcGo.UnaryHandler) (any, error)

// gRPCipeline defines the middleware pipeline to be plugged into Dapr sidecar.
type Pipeline struct {
	Handlers []Middleware
}
