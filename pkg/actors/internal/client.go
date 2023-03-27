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
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"

	"google.golang.org/grpc"
)

type client interface {
	Send(*v1pb.Host) error
	Recv() (*v1pb.PlacementOrder, error)
	Close()
}

type grpcOptionsFn func() ([]grpc.DialOption, error)

// placementClient implements the best practices when handling grpc streams
// using exclusion locks to prevent concurrent Recv and Send from channels.
// properly handle stream closing by draining the renamining events until
// receives an error.
type placementClient struct {
	// recvLock prevents concurrent access to the Recv() method.
	recvLock sync.Mutex

	// sendLock prevents CloseSend and Send being used concurrently.
	// Sends use the read lock. CloseSend uses the write lock.
	sendLock sync.RWMutex

	// stream is the client side stream.
	stream v1pb.Placement_ReportDaprStatusClient

	// conn is the gRPC client connection.
	conn *grpc.ClientConn

	closed atomic.Bool
}

func newPlacementClient(ctx context.Context, addr string, getGrpcOpts grpcOptionsFn) (client, error) {
	opts, err := getGrpcOpts()
	if err != nil {
		return nil, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	conn, err := grpc.DialContext(dialCtx, addr, opts...)
	if err != nil {
		if conn != nil {
			conn.Close()
		}
		return nil, fmt.Errorf("error connecting to placement service at %q: %s", addr, err)
	}

	stream, err := v1pb.NewPlacementClient(conn).ReportDaprStatus(ctx)
	if err != nil {
		if conn != nil {
			conn.Close()
		}
		return nil, err
	}

	return &placementClient{
		conn:   conn,
		stream: stream,
	}, nil
}

// Recv is a convenient way to call recv providing thread-safe guarantees with
// disconnections and draining operations.
func (c *placementClient) Recv() (*v1pb.PlacementOrder, error) {
	c.recvLock.Lock()
	defer c.recvLock.Unlock()
	if c.closed.Load() {
		return nil, errors.New("placement client is closed")
	}
	// cannot recv in parallel
	return c.stream.Recv()
}

// Send is a convenient way of invoking send providing thread-safe guarantees
// with `CloseSend` operations.
func (c *placementClient) Send(host *v1pb.Host) error {
	if c.closed.Load() {
		return errors.New("placement client is closed")
	}
	c.sendLock.RLock()
	defer c.sendLock.RUnlock()
	return c.stream.Send(host)
}

func (c *placementClient) Close() {
	c.closed.Store(true)

	// drain the grpc stream as described in the documentation
	// https://github.com/grpc/grpc-go/blob/be1fb4f27549f736b9b4ec26104c7c6b29845ad0/stream.go#L109
	c.sendLock.Lock()
	c.recvLock.Lock()
	defer c.sendLock.Unlock()
	defer c.recvLock.Unlock()

	c.stream.CloseSend() // CloseSend cannot be invoked concurrently with Send()
	c.conn.Close()

	for {
		if err := c.stream.RecvMsg(struct{}{}); err != nil { // recv cannot be invoked concurrently with other Recv.
			break
		}
	}
}
