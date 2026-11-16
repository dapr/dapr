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

package lock

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/targets/errors"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

func Test_Close_concurrentLockRequest(t *testing.T) {
	t.Parallel()

	for i := range 300 {
		l := New(Options{ActorType: "foo", ConfigStore: reentrancystore.New()})

		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 4 {
			wg.Go(func() {
				<-start
				ctx, release, err := l.LockRequest(t.Context(), internalv1pb.NewInternalInvokeRequest("foo"))
				if err != nil {
					assert.True(t, errors.IsClosed(err), "iteration %d: %v", i, err)
					return
				}
				release()
				<-ctx.Done()
			})
		}

		close(start)
		l.Close(t.Context())

		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "callers did not return", "iteration %d", i)
		}

		_, _, err := l.LockRequest(t.Context(), internalv1pb.NewInternalInvokeRequest("foo"))
		require.True(t, errors.IsClosed(err), "iteration %d: lock must refuse requests after Close", i)
	}
}

func Test_Close_requestMutatedWhileHeld(t *testing.T) {
	t.Parallel()

	for i := range 100 {
		l := New(Options{ActorType: "foo", ConfigStore: reentrancystore.New()})

		req := internalv1pb.NewInternalInvokeRequest("foo")
		ctx, release, err := l.LockRequest(t.Context(), req)
		require.NoError(t, err)

		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				req.Message.Method = "actors/foo/bar/method/foo"
				req.Message.Method = "foo"
			}
		})

		l.Close(t.Context())

		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			require.FailNow(t, "held request was not cancelled by Close", "iteration %d", i)
		}
		close(stop)
		wg.Wait()
		release()

		require.EqualError(t, context.Cause(ctx), "actor is closed, cannot handle foo", "iteration %d", i)
	}
}
