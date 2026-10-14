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

package operator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	loadercompstore "github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
	testinggrpc "github.com/dapr/dapr/pkg/testing/grpc"
	"github.com/dapr/kit/logger"
)

type droppingOperator struct {
	operatorpb.UnimplementedOperatorServer
	opened atomic.Int64
}

func (d *droppingOperator) drop() error {
	d.opened.Add(1)
	return nil
}

func (d *droppingOperator) ComponentUpdate(*operatorpb.ComponentUpdateRequest, operatorpb.Operator_ComponentUpdateServer) error {
	return d.drop()
}

func (d *droppingOperator) SubscriptionUpdate(*operatorpb.SubscriptionUpdateRequest, operatorpb.Operator_SubscriptionUpdateServer) error {
	return d.drop()
}

func (d *droppingOperator) HTTPEndpointUpdate(*operatorpb.HTTPEndpointUpdateRequest, operatorpb.Operator_HTTPEndpointUpdateServer) error {
	return d.drop()
}

func (d *droppingOperator) MCPServerUpdate(*operatorpb.MCPServerUpdateRequest, operatorpb.Operator_MCPServerUpdateServer) error {
	return d.drop()
}

func (d *droppingOperator) ConfigurationUpdate(*operatorpb.ConfigurationUpdateRequest, operatorpb.Operator_ConfigurationUpdateServer) error {
	return d.drop()
}

func (d *droppingOperator) ResiliencyUpdate(*operatorpb.ResiliencyUpdateRequest, operatorpb.Operator_ResiliencyUpdateServer) error {
	return d.drop()
}

func (d *droppingOperator) WorkflowAccessPolicyUpdate(*operatorpb.WorkflowAccessPolicyUpdateRequest, operatorpb.Operator_WorkflowAccessPolicyUpdateServer) error {
	return d.drop()
}

func drainConn[T differ.Resource](ctx context.Context, wg *sync.WaitGroup, conn *loader.StreamConn[T]) {
	wg.Go(func() {
		for {
			select {
			case <-conn.EventCh:
			case <-conn.ReconcileCh:
			case <-ctx.Done():
				return
			}
		}
	})
}

func Test_operator_closeDuringReconnect(t *testing.T) {
	t.Parallel()

	for i := range 10 {
		srv := new(droppingOperator)
		client, cleanup, err := testinggrpc.TestServerFor(
			logger.NewLogger("test"),
			func(s *grpc.Server, srv operatorpb.OperatorServer) {
				operatorpb.RegisterOperatorServer(s, srv)
			},
			operatorpb.NewOperatorClient,
		)(srv)
		require.NoError(t, err)

		op := New(Options{
			Namespace:      "default",
			ComponentStore: compstore.New(),
			OperatorClient: client,
		}).(*operator)

		runCtx, runCancel := context.WithCancel(t.Context())
		runErr := make(chan error, 1)
		go func() { runErr <- op.Run(runCtx) }()

		streamCtx := t.Context()
		var drain sync.WaitGroup
		drainCtx, drainCancel := context.WithCancel(t.Context())

		compConn, err := op.Components().Stream(streamCtx)
		require.NoError(t, err)
		drainConn(drainCtx, &drain, compConn)
		subConn, err := op.Subscriptions().Stream(streamCtx)
		require.NoError(t, err)
		drainConn(drainCtx, &drain, subConn)
		mcpConn, err := op.MCPServers().Stream(streamCtx)
		require.NoError(t, err)
		drainConn(drainCtx, &drain, mcpConn)
		confConn, err := op.Configurations().Stream(streamCtx)
		require.NoError(t, err)
		drainConn(drainCtx, &drain, confConn)
		httpConn, err := op.HTTPEndpoints().Stream(streamCtx)
		require.NoError(t, err)
		drainConn(drainCtx, &drain, httpConn)
		resConn, err := op.Resiliencies().Stream(streamCtx)
		require.NoError(t, err)
		drainConn(drainCtx, &drain, resConn)
		wfConn, err := op.WorkflowAccessPolicies().Stream(streamCtx)
		require.NoError(t, err)
		drainConn(drainCtx, &drain, wfConn)

		require.Eventually(t, func() bool {
			return srv.opened.Load() >= 7*5
		}, 10*time.Second, time.Millisecond, "iteration %d", i)

		runCancel()

		select {
		case err := <-runErr:
			require.NoError(t, err, "iteration %d", i)
		case <-time.After(10 * time.Second):
			require.FailNow(t, "operator loader did not close", "iteration %d", i)
		}

		drainCancel()
		drain.Wait()
		cleanup()
	}
}

//nolint:unused
type racyStreamer struct {
	current   *streamHandle
	recvFails bool

	establishCalls atomic.Int64
	closeCalls     atomic.Int64

	mu     sync.Mutex
	events []string
}

//nolint:unused
type streamHandle struct {
	ctx context.Context
}

//nolint:unused
func (r *racyStreamer) record(e string) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *racyStreamer) lastEvent() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return ""
	}
	return r.events[len(r.events)-1]
}

//nolint:unused
func (r *racyStreamer) list(context.Context, operatorpb.OperatorClient, string) ([][]byte, error) {
	return nil, nil
}

//nolint:unused
func (r *racyStreamer) close() error {
	r.closeCalls.Add(1)
	r.record("close")
	if r.current != nil {
		r.current = nil
	}
	return nil
}

//nolint:unused
func (r *racyStreamer) recv(context.Context) (*loader.Event[componentsapi.Component], error) {
	h := r.current
	if h == nil {
		return nil, errors.New("no stream")
	}
	if r.recvFails {
		return nil, errors.New("stream dropped")
	}
	<-h.ctx.Done()
	return nil, h.ctx.Err()
}

//nolint:unused
func (r *racyStreamer) establish(ctx context.Context, _ operatorpb.OperatorClient, _ string) error {
	r.establishCalls.Add(1)
	r.record("establish")
	r.current = &streamHandle{ctx: ctx}
	return nil
}

func newRacyResource(s *racyStreamer) *resource[componentsapi.Component] {
	return newResource[componentsapi.Component](
		Options{},
		loadercompstore.NewComponents(compstore.New()),
		s,
	)
}

func Test_resource_closeRace(t *testing.T) {
	t.Parallel()

	t.Run("close while the stream is reconnecting in a tight loop", func(t *testing.T) {
		t.Parallel()

		for i := range 100 {
			s := &racyStreamer{recvFails: true}
			r := newRacyResource(s)

			conn, err := r.Stream(t.Context())
			require.NoError(t, err)

			drainCtx, drainCancel := context.WithCancel(t.Context())
			var drain sync.WaitGroup
			drainConn(drainCtx, &drain, conn)

			require.Eventually(t, func() bool {
				return s.establishCalls.Load() >= 3
			}, 5*time.Second, time.Microsecond, "iteration %d", i)

			require.NoError(t, r.close(), "iteration %d", i)

			established := s.establishCalls.Load()
			assert.Equal(t, "close", s.lastEvent(),
				"iteration %d: the streamer must be closed after the last establish", i)
			assert.Equal(t, established, s.establishCalls.Load(),
				"iteration %d: no establish may run after close returned", i)

			drainCancel()
			drain.Wait()
		}
	})

	t.Run("close stops the initial stream when the stream context is never cancelled", func(t *testing.T) {
		t.Parallel()

		s := new(racyStreamer)
		r := newRacyResource(s)

		_, err := r.Stream(t.Context())
		require.NoError(t, err)

		closed := make(chan error, 1)
		go func() { closed <- r.close() }()

		select {
		case err := <-closed:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "close did not stop the initial stream")
		}
		assert.Equal(t, int64(1), s.establishCalls.Load(),
			"a stream ended by close must not be re-established")
		assert.GreaterOrEqual(t, s.closeCalls.Load(), int64(1))
		assert.Equal(t, "close", s.lastEvent())
	})

	t.Run("close aborts an establish retry loop", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int64
		streamer := newFakeStreamer()
		streamer.establishFn = func(context.Context, operatorpb.OperatorClient, string) error {
			if calls.Add(1) == 1 {
				return nil
			}
			return errors.New("operator unavailable")
		}
		streamer.recvFn = func(context.Context) (*loader.Event[componentsapi.Component], error) {
			return nil, errors.New("stream dropped")
		}
		r := newResource[componentsapi.Component](
			Options{},
			loadercompstore.NewComponents(compstore.New()),
			streamer,
		)

		_, err := r.Stream(t.Context())
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return calls.Load() >= 2
		}, 5*time.Second, time.Millisecond)

		closed := make(chan error, 1)
		go func() { closed <- r.close() }()

		select {
		case err := <-closed:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "close did not abort the establish backoff")
		}
	})

	t.Run("Stream racing close never leaks a stream goroutine", func(t *testing.T) {
		t.Parallel()

		for i := range 200 {
			s := new(racyStreamer)
			r := newRacyResource(s)

			start := make(chan struct{})
			streamErr := make(chan error, 1)
			go func() {
				<-start
				_, err := r.Stream(t.Context())
				streamErr <- err
			}()
			closeErr := make(chan error, 1)
			go func() {
				<-start
				closeErr <- r.close()
			}()
			close(start)

			select {
			case err := <-closeErr:
				require.NoError(t, err, "iteration %d", i)
			case <-time.After(5 * time.Second):
				require.FailNow(t, "close did not return", "iteration %d", i)
			}

			err := <-streamErr
			if err != nil {
				require.ErrorContains(t, err, "stream is closed", "iteration %d", i)
			}

			require.NoError(t, r.close(), "iteration %d", i)
		}
	})

	t.Run("Stream after close is rejected without establishing", func(t *testing.T) {
		t.Parallel()

		s := new(racyStreamer)
		r := newRacyResource(s)
		require.NoError(t, r.close())

		conn, err := r.Stream(t.Context())
		require.ErrorContains(t, err, "stream is closed")
		assert.Nil(t, conn)
		assert.Equal(t, int64(0), s.establishCalls.Load())
	})
}
