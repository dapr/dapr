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

package grpc

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/api/universal"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	wfenginefake "github.com/dapr/dapr/pkg/runtime/wfengine/fake"
)

func newCloseTestAPI() *api {
	return &api{
		Universal: universal.New(universal.Options{
			CompStore:      compstore.New(),
			Actors:         fake.New(),
			WorkflowEngine: wfenginefake.New(),
		}),
		closeCh: make(chan struct{}),
	}
}

type blockingSubscribeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *blockingSubscribeStream) Context() context.Context {
	return s.ctx
}

func (s *blockingSubscribeStream) Recv() (*runtimev1pb.SubscribeTopicEventsRequestAlpha1, error) {
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}

func (s *blockingSubscribeStream) Send(*runtimev1pb.SubscribeTopicEventsResponseAlpha1) error {
	return nil
}

func TestGoUnlessClosed(t *testing.T) {
	t.Run("runs fns and Close waits for them", func(t *testing.T) {
		a := newCloseTestAPI()

		release := make(chan struct{})
		var done atomic.Int32
		require.NoError(t, a.goUnlessClosed(
			func() { <-release; done.Add(1) },
			func() { <-release; done.Add(1) },
		))

		closed := make(chan struct{})
		go func() {
			assert.NoError(t, a.Close())
			close(closed)
		}()

		select {
		case <-closed:
			require.Fail(t, "Close must wait for tracked goroutines")
		case <-time.After(100 * time.Millisecond):
		}

		close(release)
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			require.Fail(t, "Close did not return after tracked goroutines finished")
		}
		assert.Equal(t, int32(2), done.Load())
	})

	t.Run("after Close returns errAPIClosed and does not run fns", func(t *testing.T) {
		a := newCloseTestAPI()
		require.NoError(t, a.Close())

		var ran atomic.Bool
		require.ErrorIs(t, a.goUnlessClosed(func() { ran.Store(true) }), errAPIClosed)
		assert.False(t, ran.Load())
	})
}

func TestCloseConcurrentSubscribeTopicEvents(t *testing.T) {
	const (
		iterations = 200
		callers    = 8
	)

	for range iterations {
		a := newCloseTestAPI()

		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(callers)
		for range callers {
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}

					ctx, cancel := context.WithCancel(t.Context())
					err := a.SubscribeTopicEventsAlpha1(&blockingSubscribeStream{ctx: ctx})
					cancel()
					if !assert.ErrorIs(t, err, errAPIClosed) {
						return
					}
				}
			}()
		}

		require.NoError(t, a.Close())
		close(stop)
		wg.Wait()
	}
}
