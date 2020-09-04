// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"github.com/dapr/dapr/pkg/config"
	grpc_go "google.golang.org/grpc"
)

type Middleware func() grpc_go.UnaryServerInterceptor

// Pipeline defines the middleware pipeline to be plugged into Dapr sidecar
type Pipeline struct {
	Interceptors []Middleware
}

func BuildGRPCPipeline(spec config.PipelineSpec) (Pipeline, error) {
	return Pipeline{}, nil
}

func (p Pipeline) Apply() []grpc_go.UnaryServerInterceptor {
	ret := make([]grpc_go.UnaryServerInterceptor, 0)
	for i := len(p.Interceptors) - 1; i >= 0; i-- {
		ret = append(ret, p.Interceptors[i]())
	}
	return ret
}
