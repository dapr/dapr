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
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"

	inmemory "github.com/dapr/components-contrib/state/in-memory"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/wfengine/inprocess"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/logger"
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

	// Every call a fallback host may stage gets a marker.
	assert.Equal(t, map[string]bool{
		"start":     true,
		"terminate": true,
		"suspend":   true,
		"resume":    true,
		"rerun":     true,
		"raise":     true,
	}, srv.marked)
}

// newFallbackTestServer returns the durabletask gRPC server of an engine
// whose actor calls are answered by r.
func newFallbackTestServer(t *testing.T, r router.Interface) protos.TaskHubSidecarServiceServer {
	t.Helper()

	cs := compstore.New()
	require.NoError(t, cs.AddStateStoreActor("mystore", inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))))

	wfe, err := New(Options{
		AppID:          "appx",
		Namespace:      "default",
		ComponentStore: cs,
		Actors: actorsfake.New().WithRouter(func(context.Context) (router.Interface, error) {
			return r, nil
		}),
		InProcessExecutor: inprocess.NewExecutor(),
	})
	require.NoError(t, err)

	var reg captureRegistrar
	wfe.(*engine).registerGrpcServerFn(&reg)
	srv, ok := reg.impl.(protos.TaskHubSidecarServiceServer)
	require.True(t, ok)
	return srv
}

// TestFallbackAwareServer_Staged checks that each operation a fallback host
// may stage reports staged on its response exactly when a fallback host
// served it.
func TestFallbackAwareServer_Staged(t *testing.T) {
	type op func(context.Context, protos.TaskHubSidecarServiceServer) (bool, error)
	ops := map[string]op{
		"start": func(ctx context.Context, srv protos.TaskHubSidecarServiceServer) (bool, error) {
			res, err := srv.StartInstance(ctx, &protos.CreateInstanceRequest{InstanceId: "wf1", Name: "wf"})
			return res.GetStaged(), err
		},
		"raise": func(ctx context.Context, srv protos.TaskHubSidecarServiceServer) (bool, error) {
			res, err := srv.RaiseEvent(ctx, &protos.RaiseEventRequest{InstanceId: "wf1", Name: "event"})
			return res.GetStaged(), err
		},
		"terminate": func(ctx context.Context, srv protos.TaskHubSidecarServiceServer) (bool, error) {
			res, err := srv.TerminateInstance(ctx, &protos.TerminateRequest{InstanceId: "wf1"})
			return res.GetStaged(), err
		},
		"suspend": func(ctx context.Context, srv protos.TaskHubSidecarServiceServer) (bool, error) {
			res, err := srv.SuspendInstance(ctx, &protos.SuspendRequest{InstanceId: "wf1"})
			return res.GetStaged(), err
		},
		"resume": func(ctx context.Context, srv protos.TaskHubSidecarServiceServer) (bool, error) {
			res, err := srv.ResumeInstance(ctx, &protos.ResumeRequest{InstanceId: "wf1"})
			return res.GetStaged(), err
		},
		"rerun": func(ctx context.Context, srv protos.TaskHubSidecarServiceServer) (bool, error) {
			res, err := srv.RerunWorkflowFromEvent(ctx, &protos.RerunWorkflowFromEventRequest{
				SourceInstanceID: "wf1",
				NewInstanceID:    new("wf2"),
			})
			return res.GetStaged(), err
		},
	}

	// A completed instance satisfies every wait the executor makes after an
	// operation a fallback host did not serve.
	completed, err := anypb.New(&backend.WorkflowMetadata{
		RuntimeStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
	})
	require.NoError(t, err)

	for _, fallback := range []bool{true, false} {
		r := routerfake.New().
			WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
				res := new(internalsv1pb.InternalInvokeResponse)
				if fallback {
					common.SetFallback(res)
				}
				return res, nil
			}).
			WithCallStreamFn(func(_ context.Context, _ *internalsv1pb.InternalInvokeRequest, fn func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
				_, err := fn(&internalsv1pb.InternalInvokeResponse{
					Message: &commonv1pb.InvokeResponse{Data: completed},
				})
				return err
			})
		srv := newFallbackTestServer(t, r)

		for name, call := range ops {
			t.Run(fmt.Sprintf("%s served by a fallback host: %t", name, fallback), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second*10)
				defer cancel()
				staged, err := call(ctx, srv)
				require.NoError(t, err)
				assert.Equal(t, fallback, staged)
			})
		}
	}
}

// TestFallbackAwareServer_CallerFeatureOff starts a workflow on another app
// through an engine which does not enable WorkflowsStagedOperations. The
// target app's flag decides whether the start is staged: a start its
// fallback host served returns once accepted, any other still waits for the
// instance to start.
func TestFallbackAwareServer_CallerFeatureOff(t *testing.T) {
	newServer := func(t *testing.T, fallback bool) (protos.TaskHubSidecarServiceServer, *atomic.Int32) {
		t.Helper()

		var watches atomic.Int32
		r := routerfake.New().
			WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
				res := new(internalsv1pb.InternalInvokeResponse)
				if fallback {
					common.SetFallback(res)
				}
				return res, nil
			}).
			// The instance never starts: no worker is connected to the
			// target app.
			WithCallStreamFn(func(ctx context.Context, _ *internalsv1pb.InternalInvokeRequest, _ func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
				watches.Add(1)
				<-ctx.Done()
				return ctx.Err()
			})

		return newFallbackTestServer(t, r), &watches
	}

	start := func(ctx context.Context, srv protos.TaskHubSidecarServiceServer) error {
		_, err := srv.StartInstance(ctx, &protos.CreateInstanceRequest{
			InstanceId: "wf1",
			Name:       "wf",
			Router:     &protos.TaskRouter{SourceAppID: "appx", TargetAppID: new("appy")},
		})
		return err
	}

	t.Run("a start a fallback host of the target app served returns at once", func(t *testing.T) {
		srv, watches := newServer(t, true)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second*10)
		defer cancel()
		require.NoError(t, start(ctx, srv))
		assert.Zero(t, watches.Load())
	})

	t.Run("any other start waits for the instance to start", func(t *testing.T) {
		srv, watches := newServer(t, false)
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond*200)
		defer cancel()
		require.ErrorIs(t, start(ctx, srv), context.DeadlineExceeded)
		assert.Equal(t, int32(1), watches.Load())
	})
}
