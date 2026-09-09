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

package actors

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/durabletask-go/api/protos"
)

// Test_registerBestEffort pins the rolling-upgrade contract: a Register that
// the executor host rejects (a daprd predating the method) must not fail the
// wait; the watch stream alone rendezvouses, as before.
func Test_registerBestEffort(t *testing.T) {
	t.Parallel()

	var registers atomic.Int32
	var watchKey atomic.Pointer[string]

	want := &protos.WorkflowResponse{InstanceId: "abc"}
	data, err := proto.Marshal(want)
	require.NoError(t, err)

	rtr := routerfake.New().
		WithCallFn(func(_ context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			assert.Equal(t, executor.MethodRegister, req.GetMessage().GetMethod())
			registers.Add(1)
			return nil, errors.New("error invoke actor method: unknown method: Register")
		}).
		WithCallStreamFn(func(_ context.Context, req *internalsv1pb.InternalInvokeRequest, stream func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
			assert.Equal(t, executor.MethodWatchComplete, req.GetMessage().GetMethod())
			key := req.GetActor().GetActorId()
			watchKey.Store(&key)
			_, serr := stream(&internalsv1pb.InternalInvokeResponse{
				Status:  &internalsv1pb.Status{Code: int32(codes.OK)},
				Message: &commonv1pb.InvokeResponse{Data: internalsv1pb.NewInternalInvokeRequest("").WithData(data).GetMessage().GetData()},
			})
			return serr
		})

	be := NewClusterTasksBackend(ClusterTasksBackendOptions{
		Actors: actorsfake.New().WithRouter(func(context.Context) (router.Interface, error) {
			return rtr, nil
		}),
		ExecutorActorType: "dapr.internal.default.app.executor",
	})

	wait := be.WaitForWorkflowTaskCompletion(&protos.WorkflowRequest{InstanceId: "abc"})
	assert.Equal(t, int32(1), registers.Load())

	got, err := wait(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "abc", got.GetInstanceId())
	require.NotNil(t, watchKey.Load())
	assert.Equal(t, "abc", *watchKey.Load())
}
