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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncfloat64"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	isemconv "github.com/dapr/dapr/pkg/diagnostics/semconv"
)

type grpcMetrics struct {
	serverReceivedBytes syncint64.Histogram
	serverSentBytes     syncint64.Histogram
	serverLatency       syncfloat64.Histogram
	serverCompletedRpcs syncint64.Counter

	clientSentBytes        syncint64.Histogram
	clientReceivedBytes    syncint64.Histogram
	clientRoundtripLatency syncfloat64.Histogram
	clientCompletedRpcs    syncint64.Counter

	healthProbeCompletedCount  syncint64.Counter
	healthProbeRoundripLatency syncfloat64.Histogram
}

func (m *MetricClient) newGRPCMetrics() *grpcMetrics {
	gm := new(grpcMetrics)
	gm.serverReceivedBytes, _ = m.meter.SyncInt64().Histogram(
		"grpc.io/server/received_bytes_per_rpc",
		instrument.WithDescription("Total bytes received across all messages per RPC."),
		instrument.WithUnit(unit.Bytes))
	gm.serverSentBytes, _ = m.meter.SyncInt64().Histogram(
		"grpc.io/server/sent_bytes_per_rpc",
		instrument.WithDescription("Total bytes sent in across all response messages per RPC."),
		instrument.WithUnit(unit.Bytes))
	gm.serverLatency, _ = m.meter.SyncFloat64().Histogram(
		"grpc.io/server/server_latency",
		instrument.WithDescription("Time between first byte of request received to last byte of response sent, or terminal error."),
		instrument.WithUnit(unit.Milliseconds))
	gm.serverCompletedRpcs, _ = m.meter.SyncInt64().Counter(
		"grpc.io/server/completed_rpcs",
		instrument.WithDescription("Distribution of bytes sent per RPC, by method."),
		instrument.WithUnit(unit.Dimensionless))
	gm.clientSentBytes, _ = m.meter.SyncInt64().Histogram(
		"grpc.io/client/sent_bytes_per_rpc",
		instrument.WithDescription("Total bytes sent across all request messages per RPC."),
		instrument.WithUnit(unit.Bytes))
	gm.clientReceivedBytes, _ = m.meter.SyncInt64().Histogram(
		"grpc.io/client/received_bytes_per_rpc",
		instrument.WithDescription("Total bytes received across all response messages per RPC."),
		instrument.WithUnit(unit.Bytes))
	gm.clientRoundtripLatency, _ = m.meter.SyncFloat64().Histogram(
		"grpc.io/client/roundtrip_latency",
		instrument.WithDescription("Time between first byte of request sent to last byte of response received, or terminal error."),
		instrument.WithUnit(unit.Milliseconds))
	gm.clientCompletedRpcs, _ = m.meter.SyncInt64().Counter(
		"grpc.io/client/completed_rpcs",
		instrument.WithDescription("Count of RPCs by method and status."),
		instrument.WithUnit(unit.Dimensionless))

	gm.healthProbeCompletedCount, _ = m.meter.SyncInt64().Counter(
		"grpc.io/healthprobes/completed_count",
		instrument.WithDescription("Count of completed health probes"),
		instrument.WithUnit(unit.Dimensionless))
	gm.healthProbeRoundripLatency, _ = m.meter.SyncFloat64().Histogram(
		"grpc.io/healthprobes/roundtrip_latency",
		instrument.WithDescription("Time between first byte of health probes sent to last byte of response received, or terminal error"),
		instrument.WithUnit(unit.Milliseconds))
	return gm
}

func (g *grpcMetrics) ServerRequestReceived(ctx context.Context, method string, contentSize int64) time.Time {
	if g == nil {
		return time.Time{}
	}
	g.serverReceivedBytes.Record(ctx, contentSize,
		isemconv.APIProtocolGRPC,
		semconv.RPCMethodKey.String(method),
		isemconv.RPCTypeServer,
	)

	return time.Now()
}

func (g *grpcMetrics) ServerRequestSent(ctx context.Context, method, status string, contentSize int64, start time.Time) {
	if g == nil {
		return
	}
	elapsed := float64(time.Since(start) / time.Millisecond)
	attributes := []attribute.KeyValue{
		isemconv.APIProtocolGRPC,
		semconv.RPCMethodKey.String(method),
		isemconv.RPCTypeServer,
		isemconv.RPCStatusKey.String(status),
	}
	g.serverCompletedRpcs.Add(ctx, 1, attributes...)
	g.serverSentBytes.Record(ctx, contentSize, attributes...)
	g.serverLatency.Record(ctx, elapsed, attributes...)
}

func (g *grpcMetrics) ClientRequestSent(ctx context.Context, method string, contentSize int64) time.Time {
	if g == nil {
		return time.Time{}
	}
	g.clientSentBytes.Record(ctx, contentSize,
		isemconv.APIProtocolGRPC,
		semconv.RPCMethodKey.String(method),
		isemconv.RPCTypeClient,
	)

	return time.Now()
}

func (g *grpcMetrics) ClientRequestReceived(ctx context.Context, method, status string, contentSize int64, start time.Time) {
	if g == nil {
		return
	}
	elapsed := float64(time.Since(start) / time.Millisecond)
	attributes := []attribute.KeyValue{
		isemconv.APIProtocolGRPC,
		semconv.RPCMethodKey.String(method),
		isemconv.RPCTypeClient,
		isemconv.RPCStatusKey.String(status),
	}
	g.clientCompletedRpcs.Add(ctx, 1, attributes...)
	g.clientRoundtripLatency.Record(ctx, elapsed, attributes...)
	g.clientReceivedBytes.Record(ctx, contentSize, attributes[:len(attributes)-1]...)
}

func (g *grpcMetrics) AppHealthProbeCompleted(ctx context.Context, status string, start time.Time) {
	if g == nil {
		return
	}

	elapsed := float64(time.Since(start) / time.Millisecond)
	attributes := []attribute.KeyValue{
		isemconv.APIProtocolGRPC,
		isemconv.RPCTypeClient,
		isemconv.RPCStatusKey.String(status),
	}
	g.healthProbeCompletedCount.Add(ctx, 1, attributes...)
	g.healthProbeRoundripLatency.Record(ctx, elapsed, attributes...)

	return
}

func (g *grpcMetrics) getPayloadSize(payload interface{}) int {
	return proto.Size(payload.(proto.Message))
}

// UnaryServerInterceptor is a gRPC server-side interceptor for Unary RPCs.
func (g *grpcMetrics) UnaryServerInterceptor() func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := g.ServerRequestReceived(ctx, info.FullMethod, int64(g.getPayloadSize(req)))
		resp, err := handler(ctx, req)
		size := 0
		if err == nil {
			size = g.getPayloadSize(resp)
		}
		g.ServerRequestSent(ctx, info.FullMethod, status.Code(err).String(), int64(size), start)
		return resp, err
	}
}

// UnaryClientInterceptor is a gRPC client-side interceptor for Unary RPCs.
func (g *grpcMetrics) UnaryClientInterceptor() func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var (
			start time.Time
			err   error
			size  int
		)

		if method == AppHealthCheckMethod {
			start = time.Now()
		} else {
			start = g.ClientRequestSent(ctx, method, int64(g.getPayloadSize(req)))
		}

		err = invoker(ctx, method, req, reply, cc, opts...)
		if err == nil {
			size = g.getPayloadSize(reply)
		}

		if method == AppHealthCheckMethod {
			g.AppHealthProbeCompleted(ctx, status.Code(err).String(), start)
		} else {
			g.ClientRequestReceived(ctx, method, status.Code(err).String(), int64(size), start)
		}

		return err
	}
}

// StreamingServerInterceptor is a stream interceptor for gRPC proxying calls that arrive from the application to Dapr
func (g *grpcMetrics) StreamingServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, _ := metadata.FromIncomingContext(ss.Context())
		vals := md.Get(DaprAppIDKey)
		if len(vals) == 0 {
			return handler(srv, ss)
		}

		now := time.Now()
		err := handler(srv, ss)
		g.StreamServerRequestSent(ss.Context(), info.FullMethod, status.Code(err).String(), now)

		return err
	}
}

// StreamingClientInterceptor is a stream interceptor for gRPC proxying calls that arrive from a remote Dapr sidecar
func (g *grpcMetrics) StreamingClientInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, _ := metadata.FromIncomingContext(ss.Context())

		vals := md.Get(DaprAppIDKey)
		if len(vals) == 0 {
			return handler(srv, ss)
		}

		now := time.Now()
		err := handler(srv, ss)
		g.StreamClientRequestSent(ss.Context(), info.FullMethod, status.Code(err).String(), now)

		return err
	}
}

func (g *grpcMetrics) StreamServerRequestSent(ctx context.Context, method, status string, start time.Time) {
	if g == nil {
		return
	}
	attributes := []attribute.KeyValue{
		isemconv.APIProtocolGRPC,
		semconv.RPCMethodKey.String(method),
		isemconv.RPCTypeServer,
		isemconv.RPCStatusKey.String(status),
	}
	elapsed := float64(time.Since(start) / time.Millisecond)
	g.serverCompletedRpcs.Add(ctx, 1, attributes...)
	g.serverLatency.Record(ctx, elapsed, attributes...)
}

func (g *grpcMetrics) StreamClientRequestSent(ctx context.Context, method, status string, start time.Time) {
	if g == nil {
		return
	}
	attributes := []attribute.KeyValue{
		isemconv.APIProtocolGRPC,
		semconv.RPCMethodKey.String(method),
		isemconv.RPCTypeClient,
		isemconv.RPCStatusKey.String(status),
	}
	elapsed := float64(time.Since(start) / time.Millisecond)
	g.clientCompletedRpcs.Add(ctx, 1, attributes...)
	g.clientRoundtripLatency.Record(ctx, elapsed, attributes...)
}
