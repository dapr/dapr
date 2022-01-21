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
	grpc_go "google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/config"
)

// UnaryServerMiddleware is an alias of UnaryServerInterceptor to try and avoid the client
// requiring google.golang.org/grpc if they don't need it and to try and align terminology
// across the codebase.
type UnaryServerMiddleware grpc_go.UnaryServerInterceptor

// Pipeline defines the middleware pipeline to be plugged into Dapr sidecar.
type Pipeline struct {
	// The first middleware will be the outer most whilst
	// last middleware will wrap the actual call handler.
	// The middleware are currently ordered based on how
	// they are defined in the config file pipeline .e.g.
	//
	// grpcPipeline:
	//  handlers:
	//  - name: firstToBeCalled
	//    type: logRequest
	//  - name: secondToBeCalled
	//    type: jwtAuth
	//    ...
	//
	UnaryServerMiddleware []UnaryServerMiddleware

	// Can extend to support other GRPC middleware types (i.e. StreamServer, UnaryClient, UnaryStream)
}

func BuildGRPCPipeline(spec config.PipelineSpec) (Pipeline, error) {
	return Pipeline{}, nil // nolint: exhaustivestruct
}

func (p Pipeline) GetUnaryServerMiddleware() []UnaryServerMiddleware {
	return p.UnaryServerMiddleware
}
