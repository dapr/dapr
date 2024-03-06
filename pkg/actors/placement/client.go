/*
Copyright 2022 The Dapr Authors
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

package placement

import (
	"context"
	"sync"

	"google.golang.org/grpc"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

// placementClient implements the best practices when handling grpc streams
// using exclusion locks to prevent concurrent Recv and Send from channels.
// properly handle stream closing by draining the renamining events until receives an error.
// and broadcasts connection results based on new connections and disconnects.
type placementClient struct {
	// getGrpcOpts are the options that should be used to connect to the placement service
	getGrpcOpts func() ([]grpc.DialOption, error)

	// recvLock prevents concurrent access to the Recv() method.
	recvLock *sync.Mutex
	// sendLock prevents CloseSend and Send being used concurrently.
	sendLock *sync.Mutex

	// clientStream is the client side stream.
	clientStream v1pb.Placement_ReportDaprStatusClient
	// clientConn is the gRPC client connection.
	clientConn *grpc.ClientConn
	// streamConnAlive is the status of stream connection alive.
	streamConnAlive bool
	// streamConnectedCond is the condition variable for goroutines waiting for or announcing
	// that the stream between runtime and placement is connected.
	streamConnectedCond *sync.Cond
}

// connectToServer initializes a new connection to the target server and if it succeeds replace the current
// stream with the connected stream.
func (c *placementClient) connectToServer(ctx context.Context, serverAddr string) error {
	opts, err := c.getGrpcOpts()
	if err != nil {
		return err
	}

	conn, err := grpc.DialContext(ctx, serverAddr, opts...)
	if err != nil {
		if conn != nil {
			conn.Close()
		}
		return err
	}

	client := v1pb.NewPlacementClient(conn)
	stream, err := client.ReportDaprStatus(ctx)
	if err != nil {
		if conn != nil {
			conn.Close()
		}
		return err
	}

	c.streamConnectedCond.L.Lock()
	c.clientStream = stream
	c.clientConn = conn
	c.streamConnAlive = true
	c.streamConnectedCond.Broadcast()
	c.streamConnectedCond.L.Unlock()
	return nil
}

// drain the grpc stream as described in the documentation
// https://github.com/grpc/grpc-go/blob/be1fb4f27549f736b9b4ec26104c7c6b29845ad0/stream.go#L109
func (c *placementClient) drain(stream grpc.ClientStream, conn *grpc.ClientConn) {
	c.sendLock.Lock()
	stream.CloseSend() // CloseSend cannot be invoked concurrently with Send()
	c.sendLock.Unlock()
	conn.Close()

	c.recvLock.Lock()
	defer c.recvLock.Unlock()
	for {
		if err := stream.RecvMsg(struct{}{}); err != nil { // recv cannot be invoked concurrently with other Recv.
			break
		}
	}
}

var noop = func() {}

// disconnect from the current server without any additional operation.
func (c *placementClient) disconnect() {
	c.disconnectFn(noop)
}

// disonnectFn disconnects from the current server providing a way to run a function inside the lock in case of new disconnection occurs.
// the function will not be executed in case of the stream is already disconnected.
func (c *placementClient) disconnectFn(insideLockFn func()) {
	c.streamConnectedCond.L.Lock()
	if !c.streamConnAlive {
		c.streamConnectedCond.Broadcast()
		c.streamConnectedCond.L.Unlock()
		return
	}

	c.drain(c.clientStream, c.clientConn)

	c.streamConnAlive = false
	c.streamConnectedCond.Broadcast()

	insideLockFn()

	c.streamConnectedCond.L.Unlock()
}

// isConnected returns if the current instance is connected to any placement server.
func (c *placementClient) isConnected() bool {
	c.streamConnectedCond.L.Lock()
	defer c.streamConnectedCond.L.Unlock()
	return c.streamConnAlive
}

// waitUntil receives a predicate that given a current stream status returns a boolean that indicates if the stream reached the desired state.
func (c *placementClient) waitUntil(predicate func(streamConnAlive bool) bool) {
	c.streamConnectedCond.L.Lock()
	for !predicate(c.streamConnAlive) {
		c.streamConnectedCond.Wait()
	}
	c.streamConnectedCond.L.Unlock()
}

// recv is a convenient way to call recv providing thread-safe guarantees with disconnections and draining operations.
func (c *placementClient) recv() (*v1pb.PlacementOrder, error) {
	c.streamConnectedCond.L.Lock()
	stream := c.clientStream
	c.streamConnectedCond.L.Unlock()
	c.recvLock.Lock()
	defer c.recvLock.Unlock()
	return stream.Recv() // cannot recv in parallel
}

// sned is a convenient way of invoking send providing thread-safe guarantees with `CloseSend` operations.
func (c *placementClient) send(host *v1pb.Host) error {
	c.streamConnectedCond.L.Lock()
	stream := c.clientStream
	c.streamConnectedCond.L.Unlock()
	c.sendLock.Lock()
	defer c.sendLock.Unlock()
	return stream.Send(host) // cannot close send and send in parallel
}

// newPlacementClient creates a new placement client for the given dial opts.
func newPlacementClient(optionGetter func() ([]grpc.DialOption, error)) *placementClient {
	return &placementClient{
		getGrpcOpts:         optionGetter,
		streamConnAlive:     false,
		streamConnectedCond: sync.NewCond(&sync.Mutex{}),
		recvLock:            &sync.Mutex{},
		sendLock:            &sync.Mutex{},
	}
}
