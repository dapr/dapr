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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/dapr/durabletask-go/api/protos"
)

// recordingServer records whether each call got a ctx carrying the fallback
// marker: a ctx derived from the caller's, rather than the caller's.
type recordingServer struct {
	protos.UnimplementedTaskHubSidecarServiceServer
	callerCtx context.Context
	marked    map[string]bool
}

func (s *recordingServer) record(ctx context.Context, name string) {
	s.marked[name] = ctx != s.callerCtx
}

func (s *recordingServer) StartInstance(ctx context.Context, _ *protos.CreateInstanceRequest) (*protos.CreateInstanceResponse, error) {
	s.record(ctx, "start")
	return nil, nil
}

func (s *recordingServer) TerminateInstance(ctx context.Context, _ *protos.TerminateRequest) (*protos.TerminateResponse, error) {
	s.record(ctx, "terminate")
	return nil, nil
}

func (s *recordingServer) SuspendInstance(ctx context.Context, _ *protos.SuspendRequest) (*protos.SuspendResponse, error) {
	s.record(ctx, "suspend")
	return nil, nil
}

func (s *recordingServer) ResumeInstance(ctx context.Context, _ *protos.ResumeRequest) (*protos.ResumeResponse, error) {
	s.record(ctx, "resume")
	return nil, nil
}

func (s *recordingServer) RerunWorkflowFromEvent(ctx context.Context, _ *protos.RerunWorkflowFromEventRequest) (*protos.RerunWorkflowFromEventResponse, error) {
	s.record(ctx, "rerun")
	return nil, nil
}

func (s *recordingServer) RaiseEvent(ctx context.Context, _ *protos.RaiseEventRequest) (*protos.RaiseEventResponse, error) {
	s.record(ctx, "raise")
	return nil, nil
}

type captureRegistrar struct {
	impl any
}

func (c *captureRegistrar) RegisterService(_ *grpc.ServiceDesc, impl any) { c.impl = impl }

func TestFallbackAwareServer(t *testing.T) {
	ctx := t.Context()
	srv := &recordingServer{callerCtx: ctx, marked: make(map[string]bool)}
	var reg captureRegistrar
	registerFallbackAware(func(s grpc.ServiceRegistrar) {
		s.RegisterService(&protos.TaskHubSidecarService_ServiceDesc, srv)
	})(&reg)

	wrapped, ok := reg.impl.(protos.TaskHubSidecarServiceServer)
	require.True(t, ok)

	_, _ = wrapped.StartInstance(ctx, nil)
	_, _ = wrapped.TerminateInstance(ctx, nil)
	_, _ = wrapped.SuspendInstance(ctx, nil)
	_, _ = wrapped.ResumeInstance(ctx, nil)
	_, _ = wrapped.RerunWorkflowFromEvent(ctx, nil)
	_, _ = wrapped.RaiseEvent(ctx, nil)

	// Every call the executor waits after gets a marker; others do not.
	assert.Equal(t, map[string]bool{
		"start":     true,
		"terminate": true,
		"suspend":   true,
		"resume":    true,
		"rerun":     true,
		"raise":     false,
	}, srv.marked)
}
