/*
Copyright 2024 The Dapr Authors
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

package client

import (
	"context"
	"fmt"
	"time"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
)

const (
	dialTimeout = 30 * time.Second
)

// Client represents a client for interacting with the scheduler.
type Client struct {
	Conn      *grpc.ClientConn
	Scheduler schedulerv1pb.SchedulerClient
	Address   string
	Security  security.Handler
}

// New returns a new scheduler client and the underlying connection.
func New(ctx context.Context, address string, sec security.Handler) (*grpc.ClientConn, schedulerv1pb.SchedulerClient, error) {
	unaryClientInterceptor := grpcRetry.UnaryClientInterceptor()

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		unaryClientInterceptor = grpcMiddleware.ChainUnaryClient(
			unaryClientInterceptor,
			diag.DefaultGRPCMonitoring.UnaryClientInterceptor(),
		)
	}

	schedulerID, err := spiffeid.FromSegments(sec.ControlPlaneTrustDomain(), "ns", sec.ControlPlaneNamespace(), "dapr-scheduler")
	if err != nil {
		return nil, nil, err
	}

	opts := []grpc.DialOption{
		grpc.WithUnaryInterceptor(unaryClientInterceptor),
		sec.GRPCDialOptionMTLS(schedulerID), grpc.WithReturnConnectionError(),
	}

	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, address, opts...)
	if err != nil {
		return nil, nil, err
	}
	return conn, schedulerv1pb.NewSchedulerClient(conn), nil
}

func (c *Client) CloseConnection() error {
	if c.Conn == nil {
		return nil // Connection is already closed
	}

	if err := c.Conn.Close(); err != nil {
		return fmt.Errorf("error closing connection: %v", err)
	}

	c.Conn = nil // Set the connection to nil to indicate it's closed
	return nil
}

// CloseAndReconnect closes the connection and reconnects the client.
func (c *Client) CloseAndReconnect(ctx context.Context) error {
	if c.Conn != nil {
		if err := c.Conn.Close(); err != nil {
			return fmt.Errorf("error closing connection: %v", err)
		}
	}
	conn, schedulerClient, err := New(ctx, c.Address, c.Security)
	if err != nil {
		return fmt.Errorf("error creating Scheduler client for address %s: %v", c.Address, err)
	}
	c.Conn = conn
	c.Scheduler = schedulerClient

	return nil
}
