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

package workflow

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

const (
	// getWorkItemsMethod is the streaming RPC a worker uses to receive work items.
	getWorkItemsMethod = "/TaskHubSidecarService/GetWorkItems"
	// getInstanceHistoryMethod is the unary RPC a worker uses to recover a full
	// history on a stateful-history cache miss.
	getInstanceHistoryMethod = "/TaskHubSidecarService/GetInstanceHistory"
)

// WorkItemObserver records the workflow work items a worker receives over its
// GetWorkItems stream, classified by whether the sidecar sent the committed
// history in full or as a stateful-history delta (a WorkflowRequest carrying a
// CachedHistory message). It is installed as a gRPC stream interceptor by
// ConnectWorkerN and is safe for concurrent use.
type WorkItemObserver struct {
	mu                      sync.Mutex
	deltas                  map[string]int
	fullSends               map[string]int
	getInstanceHistoryCalls int
}

func newWorkItemObserver() *WorkItemObserver {
	return &WorkItemObserver{
		deltas:    make(map[string]int),
		fullSends: make(map[string]int),
	}
}

func (o *WorkItemObserver) observe(wi *protos.WorkItem) {
	wr := wi.GetWorkflowRequest()
	if wr == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if wr.GetCachedHistory() != nil {
		o.deltas[wr.GetInstanceId()]++
	} else {
		o.fullSends[wr.GetInstanceId()]++
	}
}

func sumCounts(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

// Deltas returns the total number of delta (cached-history) work items received
// across all instances.
func (o *WorkItemObserver) Deltas() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return sumCounts(o.deltas)
}

// FullSends returns the total number of full-history work items received across
// all instances.
func (o *WorkItemObserver) FullSends() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return sumCounts(o.fullSends)
}

// DeltasFor returns the number of delta work items received for a single instance.
func (o *WorkItemObserver) DeltasFor(instanceID string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.deltas[instanceID]
}

// FullSendsFor returns the number of full-history work items received for a single instance.
func (o *WorkItemObserver) FullSendsFor(instanceID string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.fullSends[instanceID]
}

// GetInstanceHistoryCalls returns how many times the worker fell back to the
// GetInstanceHistory RPC, i.e. how many stateful-history cache misses it recovered
// from. In steady state with a warm cache this stays zero.
func (o *WorkItemObserver) GetInstanceHistoryCalls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.getInstanceHistoryCalls
}

func (o *WorkItemObserver) observeUnary(method string) {
	if method != getInstanceHistoryMethod {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.getInstanceHistoryCalls++
}

func (o *WorkItemObserver) unaryInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		o.observeUnary(method)
		return err
	}
}

func (o *WorkItemObserver) streamInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		cs, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil || method != getWorkItemsMethod {
			return cs, err
		}
		return &observingClientStream{ClientStream: cs, observe: o.observe}, nil
	}
}

type observingClientStream struct {
	grpc.ClientStream
	observe func(*protos.WorkItem)
}

func (o *observingClientStream) RecvMsg(m any) error {
	err := o.ClientStream.RecvMsg(m)
	if err == nil {
		if wi, ok := m.(*protos.WorkItem); ok {
			o.observe(wi)
		}
	}
	return err
}

// ConnectedWorker is a backend worker connected to a daprd through a dedicated
// gRPC connection whose lifetime the caller controls. Its Observer records the
// work items the sidecar streams to it. Use Disconnect to drop the worker's
// stream (e.g. to model a crash or reconnect); a fresh ConnectWorkerN call
// reconnects as a new, cold stream.
type ConnectedWorker struct {
	Client   *client.TaskHubGrpcClient
	Observer *WorkItemObserver

	conn   *grpc.ClientConn
	cancel context.CancelFunc
	closed bool
	mu     sync.Mutex
}

// Disconnect tears down the worker's stream and connection. It is idempotent.
func (c *ConnectedWorker) Disconnect(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.cancel()
	require.NoError(t, c.conn.Close())
}

// ManagementClient returns a backend client for daprd index 0 that is not a
// worker. See ManagementClientN.
func (w *Workflow) ManagementClient(t *testing.T, ctx context.Context) *client.TaskHubGrpcClient {
	t.Helper()
	return w.ManagementClientN(t, ctx, 0)
}

// ManagementClientN returns a backend client connected to the daprd at the given
// index for control-plane operations only (scheduling, waiting, raising events,
// fetching history). It does not start a work-item listener, so it never executes
// workflows and never advertises any worker capability. Its connection is stable
// for the lifetime of the test, independent of any ConnectedWorker, so it can
// drive instances while workers connect and disconnect around it.
func (w *Workflow) ManagementClientN(t *testing.T, ctx context.Context, index int) *client.TaskHubGrpcClient {
	t.Helper()
	require.Less(t, index, len(w.daprds), "index out of range")
	return client.NewTaskHubGrpcClient(w.daprds[index].GRPCConn(t, ctx), logger.New(t))
}

// ConnectWorker connects a worker to daprd index 0. See ConnectWorkerN.
func (w *Workflow) ConnectWorker(t *testing.T, ctx context.Context, r *task.TaskRegistry, opts ...client.TaskHubGrpcClientOption) *ConnectedWorker {
	t.Helper()
	return w.ConnectWorkerN(t, ctx, 0, r, opts...)
}

// ConnectWorkerN connects a backend worker to the daprd at the given index,
// serving the supplied task registry, through a gRPC stream interceptor that
// observes every work item it receives. Unlike BackendClientN it does not wait
// for the worker to register (use WaitForConnectedWorkersN), so tests can model
// connect/disconnect timing precisely. The worker is torn down on test cleanup.
func (w *Workflow) ConnectWorkerN(t *testing.T, ctx context.Context, index int, r *task.TaskRegistry, opts ...client.TaskHubGrpcClientOption) *ConnectedWorker {
	t.Helper()
	require.Less(t, index, len(w.daprds), "index out of range")

	obs := newWorkItemObserver()
	wctx, cancel := context.WithCancel(ctx)

	//nolint:staticcheck
	conn, err := grpc.DialContext(wctx, w.daprds[index].GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(math.MaxInt32),
			grpc.MaxCallSendMsgSize(math.MaxInt32),
		),
		grpc.WithChainStreamInterceptor(obs.streamInterceptor()),
		grpc.WithChainUnaryInterceptor(obs.unaryInterceptor()),
	)
	require.NoError(t, err)

	cl := client.NewTaskHubGrpcClient(conn, logger.New(t), opts...)
	require.NoError(t, cl.StartWorkItemListener(wctx, r))

	cw := &ConnectedWorker{Client: cl, Observer: obs, conn: conn, cancel: cancel}
	t.Cleanup(func() {
		cw.mu.Lock()
		defer cw.mu.Unlock()
		if cw.closed {
			return
		}
		cw.closed = true
		cancel()
		_ = conn.Close()
	})

	return cw
}

// WaitForConnectedWorkers waits for at least count workers to be registered with
// daprd index 0's actor runtime. See WaitForConnectedWorkersN.
func (w *Workflow) WaitForConnectedWorkers(t *testing.T, ctx context.Context, count int) {
	t.Helper()
	w.WaitForConnectedWorkersN(t, ctx, 0, count)
}

// WaitForConnectedWorkersN waits for at least count workers to be registered with
// the actor runtime of the daprd at the given index, and for the workflow actor
// runtime itself to be ready.
func (w *Workflow) WaitForConnectedWorkersN(t *testing.T, ctx context.Context, index, count int) {
	t.Helper()
	require.Less(t, index, len(w.daprds), "index out of range")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		md := w.DaprN(index).GetMetadata(c, ctx)
		if !assert.NotNil(c, md) {
			return
		}
		if !assert.NotNil(c, md.ActorRuntime) {
			return
		}
		assert.GreaterOrEqual(c, len(md.ActorRuntime.ActiveActors), 3)
		if !assert.NotNil(c, md.Workflows) {
			return
		}
		assert.GreaterOrEqual(c, md.Workflows.ConnectedWorkers, count)
	}, time.Second*60, time.Millisecond*10)
}
