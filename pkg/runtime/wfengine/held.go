/*
Copyright 2026 The Dapr Authors
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

package wfengine

import (
	"context"

	"google.golang.org/grpc"

	backendactors "github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors"
	"github.com/dapr/durabletask-go/api/protos"
)

// registerHeldAware wraps the durabletask executor's registration so that a
// start, terminate, suspend or resume that the backend holds returns once it
// is held (the WorkflowsHeldOperations preview). The executor waits for the
// instance to start or change state after each of them; the backend skips
// that wait only for an operation it held, so every other call still waits.
func registerHeldAware(register func(grpc.ServiceRegistrar)) func(grpc.ServiceRegistrar) {
	return func(s grpc.ServiceRegistrar) {
		register(heldAwareRegistrar{s})
	}
}

type heldAwareRegistrar struct {
	grpc.ServiceRegistrar
}

func (r heldAwareRegistrar) RegisterService(desc *grpc.ServiceDesc, impl any) {
	if srv, ok := impl.(protos.TaskHubSidecarServiceServer); ok {
		impl = heldAwareServer{srv}
	}
	r.ServiceRegistrar.RegisterService(desc, impl)
}

// heldAwareServer gives each waiting call a ctx that records whether the
// backend held its operation.
type heldAwareServer struct {
	protos.TaskHubSidecarServiceServer
}

func (s heldAwareServer) StartInstance(ctx context.Context, req *protos.CreateInstanceRequest) (*protos.CreateInstanceResponse, error) {
	return s.TaskHubSidecarServiceServer.StartInstance(backendactors.WithHeldMarker(ctx), req)
}

func (s heldAwareServer) TerminateInstance(ctx context.Context, req *protos.TerminateRequest) (*protos.TerminateResponse, error) {
	return s.TaskHubSidecarServiceServer.TerminateInstance(backendactors.WithHeldMarker(ctx), req)
}

func (s heldAwareServer) SuspendInstance(ctx context.Context, req *protos.SuspendRequest) (*protos.SuspendResponse, error) {
	return s.TaskHubSidecarServiceServer.SuspendInstance(backendactors.WithHeldMarker(ctx), req)
}

func (s heldAwareServer) ResumeInstance(ctx context.Context, req *protos.ResumeRequest) (*protos.ResumeResponse, error) {
	return s.TaskHubSidecarServiceServer.ResumeInstance(backendactors.WithHeldMarker(ctx), req)
}
