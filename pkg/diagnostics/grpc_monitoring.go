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

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/grpc/metadata"
)

// This implementation is inspired by
// https://github.com/census-instrumentation/opencensus-go/tree/master/plugin/ocgrpc

// Tag key definitions for http requests.
var (
	KeyServerMethod = tag.MustNewKey("grpc_server_method")
	KeyServerStatus = tag.MustNewKey("grpc_server_status")

	KeyClientMethod = tag.MustNewKey("grpc_client_method")
	KeyClientStatus = tag.MustNewKey("grpc_client_status")
)

const appHealthCheckMethod = "/dapr.proto.runtime.v1.AppCallbackHealthCheck/HealthCheck"

type grpcMetrics struct {
	serverReceivedBytes *stats.Int64Measure
	serverSentBytes     *stats.Int64Measure
	serverLatency       *stats.Float64Measure
	serverCompletedRpcs *stats.Int64Measure

	clientSentBytes        *stats.Int64Measure
	clientReceivedBytes    *stats.Int64Measure
	clientRoundtripLatency *stats.Float64Measure
	clientCompletedRpcs    *stats.Int64Measure

	healthProbeCompletedCount  *stats.Int64Measure
	healthProbeRoundripLatency *stats.Float64Measure

	appID    string
	enabled  bool
	clock    clock.Clock
	meter    view.Meter
	regRules utils.Rules
}

func newGRPCMetrics(meter view.Meter, clock clock.Clock, regRules utils.Rules) *grpcMetrics {
	return &grpcMetrics{
		meter:    meter,
		clock:    clock,
		regRules: regRules,
		serverReceivedBytes: stats.Int64(
			"grpc.io/server/received_bytes_per_rpc",
			"Total bytes received across all messages per RPC.",
			stats.UnitBytes),
		serverSentBytes: stats.Int64(
			"grpc.io/server/sent_bytes_per_rpc",
			"Total bytes sent in across all response messages per RPC.",
			stats.UnitBytes),
		serverLatency: stats.Float64(
			"grpc.io/server/server_latency",
			"Time between first byte of request received to last byte of response sent, or terminal error.",
			stats.UnitMilliseconds),
		serverCompletedRpcs: stats.Int64(
			"grpc.io/server/completed_rpcs",
			"Distribution of bytes sent per RPC, by method.",
			stats.UnitDimensionless),

		clientSentBytes: stats.Int64(
			"grpc.io/client/sent_bytes_per_rpc",
			"Total bytes sent across all request messages per RPC.",
			stats.UnitBytes),
		clientReceivedBytes: stats.Int64(
			"grpc.io/client/received_bytes_per_rpc",
			"Total bytes received across all response messages per RPC.",
			stats.UnitBytes),
		clientRoundtripLatency: stats.Float64(
			"grpc.io/client/roundtrip_latency",
			"Time between first byte of request sent to last byte of response received, or terminal error.",
			stats.UnitMilliseconds),
		clientCompletedRpcs: stats.Int64(
			"grpc.io/client/completed_rpcs",
			"Count of RPCs by method and status.",
			stats.UnitDimensionless),

		healthProbeCompletedCount: stats.Int64(
			"grpc.io/healthprobes/completed_count",
			"Count of completed health probes",
			stats.UnitDimensionless),
		healthProbeRoundripLatency: stats.Float64(
			"grpc.io/healthprobes/roundtrip_latency",
			"Time between first byte of health probes sent to last byte of response received, or terminal error",
			stats.UnitMilliseconds),

		enabled: false,
	}
}

func (g *grpcMetrics) init(appID string) error {
	g.appID = appID
	g.enabled = true

	return g.meter.Register(
		utils.NewMeasureView(g.serverReceivedBytes, []tag.Key{appIDKey, KeyServerMethod}, defaultSizeDistribution()),
		utils.NewMeasureView(g.serverSentBytes, []tag.Key{appIDKey, KeyServerMethod}, defaultSizeDistribution()),
		utils.NewMeasureView(g.serverLatency, []tag.Key{appIDKey, KeyServerMethod}, defaultLatencyDistribution()),
		utils.NewMeasureView(g.serverCompletedRpcs, []tag.Key{appIDKey, KeyServerMethod, KeyServerStatus}, utils.Count()),
		utils.NewMeasureView(g.clientSentBytes, []tag.Key{appIDKey, KeyClientMethod}, defaultSizeDistribution()),
		utils.NewMeasureView(g.clientReceivedBytes, []tag.Key{appIDKey, KeyClientMethod}, defaultSizeDistribution()),
		utils.NewMeasureView(g.clientRoundtripLatency, []tag.Key{appIDKey, KeyClientMethod, KeyClientStatus}, defaultLatencyDistribution()),
		utils.NewMeasureView(g.clientCompletedRpcs, []tag.Key{appIDKey, KeyClientMethod, KeyClientStatus}, utils.Count()),
		utils.NewMeasureView(g.healthProbeRoundripLatency, []tag.Key{appIDKey, KeyClientStatus}, defaultLatencyDistribution()),
		utils.NewMeasureView(g.healthProbeCompletedCount, []tag.Key{appIDKey, KeyClientStatus}, utils.Count()),
	)
}

func (g *grpcMetrics) IsEnabled() bool {
	return g.enabled
}

func (g *grpcMetrics) ServerRequestReceived(ctx context.Context, method string, contentSize int64) time.Time {
	if g.enabled {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.serverReceivedBytes.Name(), appIDKey, g.appID, KeyServerMethod, method),
			stats.WithMeasurements(g.serverReceivedBytes.M(contentSize)),
		)
	}

	return g.clock.Now()
}

func (g *grpcMetrics) ServerRequestSent(ctx context.Context, method, status string, contentSize int64, start time.Time) {
	if g.enabled {
		elapsed := float64(g.clock.Since(start) / time.Millisecond)
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.serverCompletedRpcs.Name(), appIDKey, g.appID, KeyServerMethod, method, KeyServerStatus, status),
			stats.WithMeasurements(g.serverCompletedRpcs.M(1)),
		)
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.serverSentBytes.Name(), appIDKey, g.appID, KeyServerMethod, method),
			stats.WithMeasurements(g.serverSentBytes.M(contentSize)),
		)
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.serverLatency.Name(), appIDKey, g.appID, KeyServerMethod, method, KeyServerStatus, status),
			stats.WithMeasurements(g.serverLatency.M(elapsed)),
		)
	}
}

func (g *grpcMetrics) StreamServerRequestSent(ctx context.Context, method, status string, start time.Time) {
	if g.enabled {
		elapsed := float64(g.clock.Since(start) / time.Millisecond)
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.serverCompletedRpcs.Name(), appIDKey, g.appID, KeyServerMethod, method, KeyServerStatus, status),
			stats.WithMeasurements(g.serverCompletedRpcs.M(1)),
		)
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.serverLatency.Name(), appIDKey, g.appID, KeyServerMethod, method, KeyServerStatus, status),
			stats.WithMeasurements(g.serverLatency.M(elapsed)),
		)
	}
}

func (g *grpcMetrics) StreamClientRequestSent(ctx context.Context, method, status string, start time.Time) {
	if g.enabled {
		elapsed := float64(g.clock.Since(start) / time.Millisecond)
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.clientCompletedRpcs.Name(), appIDKey, g.appID, KeyClientMethod, method, KeyClientStatus, status),
			stats.WithMeasurements(g.clientCompletedRpcs.M(1)),
		)
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.clientRoundtripLatency.Name(), appIDKey, g.appID, KeyClientMethod, method, KeyClientStatus, status),
			stats.WithMeasurements(g.clientRoundtripLatency.M(elapsed)),
		)
	}
}

func (g *grpcMetrics) ClientRequestSent(ctx context.Context, method string, contentSize int64) time.Time {
	if g.enabled {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.clientSentBytes.Name(), appIDKey, g.appID, KeyClientMethod, method),
			stats.WithMeasurements(g.clientSentBytes.M(contentSize)),
		)
	}

	return g.clock.Now()
}

func (g *grpcMetrics) ClientRequestReceived(ctx context.Context, method, status string, contentSize int64, start time.Time) {
	if g.enabled {
		elapsed := float64(g.clock.Since(start) / time.Millisecond)
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.clientCompletedRpcs.Name(), appIDKey, g.appID, KeyClientMethod, method, KeyClientStatus, status),
			stats.WithMeasurements(g.clientCompletedRpcs.M(1)),
		)
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.clientRoundtripLatency.Name(), appIDKey, g.appID, KeyClientMethod, method, KeyClientStatus, status),
			stats.WithMeasurements(g.clientRoundtripLatency.M(elapsed)),
		)
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.clientReceivedBytes.Name(), appIDKey, g.appID, KeyClientMethod, method),
			stats.WithMeasurements(g.clientReceivedBytes.M(contentSize)),
		)
	}
}

func (g *grpcMetrics) AppHealthProbeStarted(ctx context.Context) time.Time {
	if g.enabled {
		stats.RecordWithOptions(ctx, stats.WithRecorder(g.meter), g.regRules.WithTags("", appIDKey, g.appID))
	}

	return g.clock.Now()
}

func (g *grpcMetrics) AppHealthProbeCompleted(ctx context.Context, status string, start time.Time) {
	if g.enabled {
		elapsed := float64(g.clock.Since(start) / time.Millisecond)
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.healthProbeCompletedCount.Name(), g.appID, KeyClientStatus, status),
			stats.WithMeasurements(g.healthProbeCompletedCount.M(1)),
		)
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(g.meter),
			g.regRules.WithTags(g.healthProbeRoundripLatency.Name(), g.appID, KeyClientStatus, status),
			stats.WithMeasurements(g.healthProbeRoundripLatency.M(elapsed)),
		)
	}
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

		if method == appHealthCheckMethod {
			start = g.AppHealthProbeStarted(ctx)
		} else {
			start = g.ClientRequestSent(ctx, method, int64(g.getPayloadSize(req)))
		}

		err = invoker(ctx, method, req, reply, cc, opts...)
		if err == nil {
			size = g.getPayloadSize(reply)
		}

		if method == appHealthCheckMethod {
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
		ctx := ss.Context()
		md, _ := metadata.FromIncomingContext(ctx)
		vals, ok := md[GRPCProxyAppIDKey]
		if !ok || len(vals) == 0 {
			return handler(srv, ss)
		}

		now := g.clock.Now()
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

		now := g.clock.Now()
		err := handler(srv, ss)
		g.StreamClientRequestSent(ctx, info.FullMethod, status.Code(err).String(), now)

		return err
	}
}
