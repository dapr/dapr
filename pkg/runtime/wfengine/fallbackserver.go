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

// registerFallbackAware wraps the durabletask executor's registration so that
// a start, rerun, terminate, suspend or resume served by a fallback host
// returns once it is accepted (the WorkflowsStagedOperations preview). The
// executor waits for the instance to start or change state after each of
// them, which on a fallback host happens only once the instance moves to a
// daprd with an executor; the backend skips that wait only for an operation
// a fallback host served, so every other call still waits. It is installed
// whether or not this app enables the preview: an operation targeting
// another app is staged on that app's fallback host if that app enables it,
// and with the preview off everywhere no response carries the marker, so
// every call waits as before.
func registerFallbackAware(register func(grpc.ServiceRegistrar)) func(grpc.ServiceRegistrar) {
	return func(s grpc.ServiceRegistrar) {
		register(fallbackAwareRegistrar{s})
	}
}

type fallbackAwareRegistrar struct {
	grpc.ServiceRegistrar
}

func (r fallbackAwareRegistrar) RegisterService(desc *grpc.ServiceDesc, impl any) {
	if srv, ok := impl.(protos.TaskHubSidecarServiceServer); ok {
		impl = fallbackAwareServer{srv}
	}
	r.ServiceRegistrar.RegisterService(desc, impl)
}

// fallbackAwareServer gives each waiting call a ctx that records whether a
// fallback host served its operation.
type fallbackAwareServer struct {
	protos.TaskHubSidecarServiceServer
}

func (s fallbackAwareServer) StartInstance(ctx context.Context, req *protos.CreateInstanceRequest) (*protos.CreateInstanceResponse, error) {
	return s.TaskHubSidecarServiceServer.StartInstance(backendactors.WithFallbackMarker(ctx), req)
}

func (s fallbackAwareServer) RerunWorkflowFromEvent(ctx context.Context, req *protos.RerunWorkflowFromEventRequest) (*protos.RerunWorkflowFromEventResponse, error) {
	return s.TaskHubSidecarServiceServer.RerunWorkflowFromEvent(backendactors.WithFallbackMarker(ctx), req)
}

func (s fallbackAwareServer) TerminateInstance(ctx context.Context, req *protos.TerminateRequest) (*protos.TerminateResponse, error) {
	return s.TaskHubSidecarServiceServer.TerminateInstance(backendactors.WithFallbackMarker(ctx), req)
}

func (s fallbackAwareServer) SuspendInstance(ctx context.Context, req *protos.SuspendRequest) (*protos.SuspendResponse, error) {
	return s.TaskHubSidecarServiceServer.SuspendInstance(backendactors.WithFallbackMarker(ctx), req)
}

func (s fallbackAwareServer) ResumeInstance(ctx context.Context, req *protos.ResumeRequest) (*protos.ResumeResponse, error) {
	return s.TaskHubSidecarServiceServer.ResumeInstance(backendactors.WithFallbackMarker(ctx), req)
}
