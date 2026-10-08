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

package executor

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	targeterrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

func Test_Deactivate_concurrentInvocations(t *testing.T) {
	t.Parallel()

	for i := range 300 {
		e, f := newClaimTestExecutor(t)

		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 4 {
			wg.Go(func() {
				<-start
				_, err := e.InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
				assert.NoError(t, err, "iteration %d", i)
			})
		}
		wg.Go(func() {
			<-start
			assert.NoError(t, e.Deactivate(t.Context()), "iteration %d", i)
		})
		close(start)
		wg.Wait()

		assert.True(t, e.closed.Load(), "iteration %d", i)
		_, ok := f.table.Load("abc")
		assert.False(t, ok, "iteration %d", i)
	}
}

func Test_Deactivate_refusesLaterCalls(t *testing.T) {
	t.Parallel()

	e, _ := newClaimTestExecutor(t)
	require.NoError(t, e.Deactivate(t.Context()))

	unknown := internalsv1pb.NewInternalInvokeRequest("unknown").
		WithActor("dapr.internal.default.test.executor", "abc")

	_, err := e.InvokeMethod(t.Context(), unknown)
	assert.True(t, targeterrors.IsClosed(err), "got %v", err)

	err = e.InvokeStream(t.Context(), unknown, nil)
	assert.True(t, targeterrors.IsClosed(err), "got %v", err)
}

// A cancellation recorded before deactivation is handed off, so a claim on the
// closed actor must not also serve it.
func Test_Deactivate_claimDoesNotServeHandedOffCancel(t *testing.T) {
	t.Parallel()

	e, _ := newClaimTestExecutor(t)
	_, err := e.InvokeMethod(t.Context(), cancelReq(TaskTypeWorkflow))
	require.NoError(t, err)

	_, canc := e.deactivate()
	require.Equal(t, TaskTypeWorkflow, canc.taskType)

	res, err := e.InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
	require.NoError(t, err)
	assert.Equal(t, int32(codes.NotFound), res.GetStatus().GetCode())
}
