/*
Copyright 2021 The Dapr Authors
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

package diagnostics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/api/grpc/metadata"
)

// This implementation is inspired by
// https://github.com/census-instrumentation/opencensus-go/tree/master/plugin/ocgrpc

// Tag key definitions for http requests.
var (
	KeyServerMethod = "grpc_server_method"
	KeyServerStatus = "grpc_server_status"
	KeyClientMethod = "grpc_client_method"
	KeyClientStatus = "grpc_client_status"
)

const appHealthCheckMethod = "/dapr.proto.runtime.v1.AppCallbackHealthCheck/HealthCheck"

type grpcMetrics struct {
	meter metric.Meter

	serverReceivedBytes metric.Int64Counter
	serverSentBytes     metric.Int64Counter
	serverLatency       metric.Float64Histogram
	serverCompletedRpcs metric.Int64Counter

	clientSentBytes        metric.Int64Counter
	clientReceivedBytes    metric.Int64Counter
	clientRoundtripLatency metric.Float64Histogram
	clientCompletedRpcs    metric.Int64Counter

	healthProbeCompletedCount  metric.Int64Counter
	healthProbeRoundripLatency metric.Float64Histogram

	appID   string
	enabled bool
}

func newGRPCMetrics() *grpcMetrics {
	m := otel.Meter("grpc")

	return &grpcMetrics{
		meter:   m,
		enabled: false,
	}
}

func (g *grpcMetrics) Init(appID string) error {
	serverReceivedBytes, err := g.meter.Int64Counter(
		"grpc.io.server.received_bytes_per_rpc",
		metric.WithDescription("Total bytes received across all messages per RPC."),
	)
	if err != nil {
		return err
	}

	serverSentBytes, err := g.meter.Int64Counter(
		"grpc.io.server.sent_bytes_per_rpc",
		metric.WithDescription("Total bytes sent in across all response messages per RPC."),
	)
	if err != nil {
		return err
	}

	serverLatency, err := g.meter.Float64Histogram(
		"grpc.io.server.server_latency",
		metric.WithDescription("Latency distribution of server-side processing time."),
	)
	if err != nil {
		return err
	}

	serverCompletedRpcs, err := g.meter.Int64Counter(
		"grpc.io.server.completed_rpcs",
		metric.WithDescription("Total number of completed RPCs on the server."),
	)
	if err != nil {
		return err
	}

	clientSentBytes, err := g.meter.Int64Counter(
		"grpc.io.client.sent_bytes_per_rpc",
		metric.WithDescription("Total bytes sent across all request messages per RPC."),
	)
	if err != nil {
		return err
	}

	clientReceivedBytes, err := g.meter.Int64Counter(
		"grpc.io.client.received_bytes_per_rpc",
		metric.WithDescription("Total bytes received across all response messages per RPC."),
	)
	if err != nil {
		return err
	}

	clientRoundtripLatency, err := g.meter.Float64Histogram(
		"grpc.io.client.roundtrip_latency",
		metric.WithDescription("Latency distribution of roundtrip time for client-side RPCs."),
	)
	if err != nil {
		return err
	}

	clientCompletedRpcs, err := g.meter.Int64Counter(
		"grpc.io.client.completed_rpcs",
		metric.WithDescription("Total number of completed RPCs on the client."),
	)
	if err != nil {
		return err
	}

	healthProbeCompletedCount, err := g.meter.Int64Counter(
		"grpc.io.health_probe.completed_count",
		metric.WithDescription("Total number of completed health probe requests."),
	)
	if err != nil {
		return err
	}

	healthProbeRoundripLatency, err := g.meter.Float64Histogram(
		"grpc.io.health_probe.roundtrip_latency",
		metric.WithDescription("Latency distribution of roundtrip time for health probe requests."),
	)
	if err != nil {
		return err
	}

	g.serverSentBytes = serverSentBytes
	g.serverReceivedBytes = serverReceivedBytes
	g.serverLatency = serverLatency
	g.serverCompletedRpcs = serverCompletedRpcs
	g.clientSentBytes = clientSentBytes
	g.clientReceivedBytes = clientReceivedBytes
	g.clientRoundtripLatency = clientRoundtripLatency
	g.clientCompletedRpcs = clientCompletedRpcs
	g.healthProbeCompletedCount = healthProbeCompletedCount
	g.healthProbeRoundripLatency = healthProbeRoundripLatency

	return nil
}

func (g *grpcMetrics) IsEnabled() bool {
	return g != nil && g.enabled
}

func (g *grpcMetrics) ServerRequestSent(ctx context.Context, method, status string, reqContentSize, resContentSize int64, start time.Time) {
	if !g.IsEnabled() {
		return
	}

	elapsed := float64(time.Since(start) / time.Millisecond)
	g.serverCompletedRpcs.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyServerMethod, method), attribute.String(KeyServerStatus, status)))
	g.serverReceivedBytes.Add(ctx, reqContentSize, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyServerMethod, method)))
	g.serverSentBytes.Add(ctx, resContentSize, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyServerMethod, method)))
	g.serverLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyServerMethod, method), attribute.String(KeyServerStatus, status)))
}

func (g *grpcMetrics) StreamServerRequestSent(ctx context.Context, method, status string, start time.Time) {
	if !g.IsEnabled() {
		return
	}

	elapsed := float64(time.Since(start) / time.Millisecond)
	g.serverCompletedRpcs.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyServerMethod, method), attribute.String(KeyServerStatus, status)))
	g.serverLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyServerMethod, method), attribute.String(KeyServerStatus, status)))
}

func (g *grpcMetrics) StreamClientRequestSent(ctx context.Context, method, status string, start time.Time) {
	if !g.IsEnabled() {
		return
	}

	elapsed := float64(time.Since(start) / time.Millisecond)
	g.clientCompletedRpcs.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyClientMethod, method), attribute.String(KeyClientStatus, status)))
	g.clientRoundtripLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyClientMethod, method), attribute.String(KeyClientStatus, status)))
}

func (g *grpcMetrics) ClientRequestReceived(ctx context.Context, method, status string, reqContentSize, resContentSize int64, start time.Time) {
	if !g.IsEnabled() {
		return
	}

	elapsed := float64(time.Since(start) / time.Millisecond)
	g.clientCompletedRpcs.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyClientMethod, method), attribute.String(KeyClientStatus, status)))
	g.clientRoundtripLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyClientMethod, method), attribute.String(KeyClientStatus, status)))
	g.clientSentBytes.Add(ctx, reqContentSize, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyClientMethod, method)))
	g.clientReceivedBytes.Add(ctx, resContentSize, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyClientMethod, method)))
}

func (g *grpcMetrics) AppHealthProbeCompleted(ctx context.Context, status string, start time.Time) {
	if !g.IsEnabled() {
		return
	}

	elapsed := float64(time.Since(start) / time.Millisecond)
	g.healthProbeCompletedCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyClientStatus, status)))
	g.healthProbeRoundripLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, g.appID), attribute.String(KeyClientStatus, status)))
}

func (g *grpcMetrics) getPayloadSize(payload interface{}) int {
	return proto.Size(payload.(proto.Message))
}

// UnaryServerInterceptor is a gRPC server-side interceptor for Unary RPCs.
func (g *grpcMetrics) UnaryServerInterceptor() func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		size := 0
		if err == nil {
			size = g.getPayloadSize(resp)
		}
		g.ServerRequestSent(ctx, info.FullMethod, status.Code(err).String(), int64(g.getPayloadSize(req)), int64(size), start)
		return resp, err
	}
}

// UnaryClientInterceptor is a gRPC client-side interceptor for Unary RPCs.
func (g *grpcMetrics) UnaryClientInterceptor() func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		start := time.Now()
		err := invoker(ctx, method, req, reply, cc, opts...)

		var resSize int
		if err == nil {
			resSize = g.getPayloadSize(reply)
		}

		if method == appHealthCheckMethod {
			g.AppHealthProbeCompleted(ctx, status.Code(err).String(), start)
		} else {
			g.ClientRequestReceived(ctx, method, status.Code(err).String(), int64(g.getPayloadSize(req)), int64(resSize), start)
		}

		return err
	}
}

// StreamingServerInterceptor is a stream interceptor for gRPC proxying calls that arrive from the application to Dapr
func (g *grpcMetrics) StreamingServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		md, _ := metadata.FromIncomingContext(ctx)
		vals, ok := md[GRPCProxyAppIDKey]
		if !ok || len(vals) == 0 {
			return handler(srv, ss)
		}

		now := time.Now()
		err := handler(srv, ss)
		g.StreamServerRequestSent(ctx, info.FullMethod, status.Code(err).String(), now)

		return err
	}
}

// StreamingClientInterceptor is a stream interceptor for gRPC proxying calls that arrive from a remote Dapr sidecar
func (g *grpcMetrics) StreamingClientInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		md, _ := metadata.FromIncomingContext(ctx)
		vals, ok := md[GRPCProxyAppIDKey]
		if !ok || len(vals) == 0 {
			return handler(srv, ss)
		}

		now := time.Now()
		err := handler(srv, ss)
		g.StreamClientRequestSent(ctx, info.FullMethod, status.Code(err).String(), now)

		return err
	}
}
