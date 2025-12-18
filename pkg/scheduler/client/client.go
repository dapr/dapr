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
	"math"
	"time"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
)

// New returns a new scheduler client.
func New(ctx context.Context, address string, sec security.Handler) (schedulerv1pb.SchedulerClient, context.CancelFunc, error) {
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
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(math.MaxInt32)),
		grpc.WithUnaryInterceptor(unaryClientInterceptor),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    time.Second * 3,
			Timeout: time.Second * 5,
		}),
		sec.GRPCDialOptionMTLS(schedulerID),
	}

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, address, opts...)
	if err != nil {
		return nil, nil, err
	}

	return schedulerv1pb.NewSchedulerClient(conn), func() { conn.Close() }, nil
}
