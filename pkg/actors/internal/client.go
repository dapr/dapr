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

package internal

import (
	"context"
	"sync"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"

	"google.golang.org/grpc"
)

type placementClient struct {
	// getGrpcOpts are the options that should be used to connect to the placement service
	getGrpcOpts func() ([]grpc.DialOption, error)

	streamMu *sync.RWMutex
	closeMu  *sync.RWMutex

	// clientStream is the client side stream.
	clientStream v1pb.Placement_ReportDaprStatusClient
	// clientConn is the gRPC client connection.
	clientConn *grpc.ClientConn
	// streamConnAlive is the status of stream connection alive.
	streamConnAlive bool
	// streamConnectedCond is the condition variable for goroutines waiting for or announcing
	// that the stream between runtime and placement is connected.
	streamConnectedCond *sync.Cond

	// ctx is the stream context
	ctx context.Context
	// cancel is the cancel func for context
	cancel context.CancelFunc
}

// connectToServer initializes a new connection to the target server and if it succeeds replace the current
// stream with the connected stream.
func (c *placementClient) connectToServer(serverAddr string) error {
	opts, err := c.getGrpcOpts()
	if err != nil {
		return err
	}

	conn, err := grpc.Dial(serverAddr, opts...)
	if err != nil {
		if conn != nil {
			conn.Close()
		}
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	client := v1pb.NewPlacementClient(conn)
	stream, err := client.ReportDaprStatus(ctx)
	if err != nil {
		cancel()
		return err
	}

	c.streamConnectedCond.L.Lock()
	c.streamMu.Lock()
	c.closeMu.Lock()
	c.ctx, c.cancel, c.clientStream, c.clientConn = ctx, cancel, stream, conn
	c.closeMu.Unlock()
	c.streamMu.Unlock()
	c.streamConnAlive = true
	c.streamConnectedCond.Broadcast()
	c.streamConnectedCond.L.Unlock()
	return nil
}

// drain the grpc stream as described in the documentation
// https://github.com/grpc/grpc-go/blob/be1fb4f27549f736b9b4ec26104c7c6b29845ad0/stream.go#L109
func (c *placementClient) drain(stream grpc.ClientStream, cancelFunc context.CancelFunc) {
	stream.CloseSend()
	cancelFunc()

	for {
		if err := stream.RecvMsg(struct{}{}); err != nil {
			break
		}
	}
}

func (c *placementClient) disconnect() {
	c.closeMu.RLock()
	c.clientConn.Close()
	c.closeMu.RUnlock()

	c.streamConnectedCond.L.Lock()
	defer c.streamConnectedCond.L.Unlock()
	if !c.streamConnAlive {
		return
	}

	c.drain(c.clientStream, c.cancel)

	c.streamConnAlive = false
	c.streamConnectedCond.Broadcast()
}

func (c *placementClient) isConnected() bool {
	c.streamConnectedCond.L.Lock()
	defer c.streamConnectedCond.L.Unlock()
	return c.streamConnAlive
}

func (c *placementClient) waitWhile(predicate func(streamConnAlive bool) bool) {
	c.streamConnectedCond.L.Lock()
	for !predicate(c.streamConnAlive) {
		c.streamConnectedCond.Wait()
	}
	c.streamConnectedCond.L.Unlock()
}

func (c *placementClient) recv() (*v1pb.PlacementOrder, error) {
	c.streamConnectedCond.L.Lock()
	defer c.streamConnectedCond.L.Unlock()
	return c.clientStream.Recv() // cannot recv in parallel
}

func (c *placementClient) send(host *v1pb.Host) error {
	c.streamConnectedCond.L.Lock()
	defer c.streamConnectedCond.L.Unlock()
	return c.clientStream.Send(host) // cannot close send and send in parallel
}

func newPlacementClient(optionGetter func() ([]grpc.DialOption, error)) *placementClient {
	return &placementClient{
		getGrpcOpts:         optionGetter,
		streamConnAlive:     false,
		streamConnectedCond: sync.NewCond(&sync.Mutex{}),
		closeMu:             &sync.RWMutex{},
		streamMu:            &sync.RWMutex{},
	}
}
