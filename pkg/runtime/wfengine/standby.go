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

// registerStandbyAware wraps the durabletask executor's registration so that a
// start, rerun, terminate, suspend or resume served by a standby host returns
// once it is accepted (the WorkflowsStandbyHosts preview). The executor waits
// for the instance to start or change state after each of them, which on a standby
// host happens only once a worker connects; the backend skips that wait only
// for an operation a standby host served, so every other call still waits.
func registerStandbyAware(register func(grpc.ServiceRegistrar)) func(grpc.ServiceRegistrar) {
	return func(s grpc.ServiceRegistrar) {
		register(standbyAwareRegistrar{s})
	}
}

type standbyAwareRegistrar struct {
	grpc.ServiceRegistrar
}

func (r standbyAwareRegistrar) RegisterService(desc *grpc.ServiceDesc, impl any) {
	if srv, ok := impl.(protos.TaskHubSidecarServiceServer); ok {
		impl = standbyAwareServer{srv}
	}
	r.ServiceRegistrar.RegisterService(desc, impl)
}

// standbyAwareServer gives each waiting call a ctx that records whether a
// standby host served its operation.
type standbyAwareServer struct {
	protos.TaskHubSidecarServiceServer
}

func (s standbyAwareServer) StartInstance(ctx context.Context, req *protos.CreateInstanceRequest) (*protos.CreateInstanceResponse, error) {
	return s.TaskHubSidecarServiceServer.StartInstance(backendactors.WithStandbyMarker(ctx), req)
}

func (s standbyAwareServer) RerunWorkflowFromEvent(ctx context.Context, req *protos.RerunWorkflowFromEventRequest) (*protos.RerunWorkflowFromEventResponse, error) {
	return s.TaskHubSidecarServiceServer.RerunWorkflowFromEvent(backendactors.WithStandbyMarker(ctx), req)
}

func (s standbyAwareServer) TerminateInstance(ctx context.Context, req *protos.TerminateRequest) (*protos.TerminateResponse, error) {
	return s.TaskHubSidecarServiceServer.TerminateInstance(backendactors.WithStandbyMarker(ctx), req)
}

func (s standbyAwareServer) SuspendInstance(ctx context.Context, req *protos.SuspendRequest) (*protos.SuspendResponse, error) {
	return s.TaskHubSidecarServiceServer.SuspendInstance(backendactors.WithStandbyMarker(ctx), req)
}

func (s standbyAwareServer) ResumeInstance(ctx context.Context, req *protos.ResumeRequest) (*protos.ResumeResponse, error) {
	return s.TaskHubSidecarServiceServer.ResumeInstance(backendactors.WithStandbyMarker(ctx), req)
}
