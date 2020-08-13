// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"time"

	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/golang/protobuf/proto"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// This implementation is inspired by
// https://github.com/census-instrumentation/opencensus-go/tree/master/plugin/ocgrpc

// Tag key definitions for http requests
var (
	KeyServerMethod = tag.MustNewKey("grpc_server_method")
	KeyServerStatus = tag.MustNewKey("grpc_server_status")

	KeyClientMethod = tag.MustNewKey("grpc_client_method")
	KeyClientStatus = tag.MustNewKey("grpc_client_status")
)

type grpcMetrics struct {
	serverReceivedBytes *stats.Int64Measure
	serverSentBytes     *stats.Int64Measure
	serverLatency       *stats.Float64Measure

	clientSentBytes        *stats.Int64Measure
	clientReceivedBytes    *stats.Int64Measure
	clientRoundtripLatency *stats.Float64Measure

	appID   string
	enabled bool
}

func newGRPCMetrics() *grpcMetrics {
	return &grpcMetrics{
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

		enabled: false,
	}
}

func (g *grpcMetrics) Init(appID string) error {
	g.appID = appID
	g.enabled = true

	views := []*view.View{
		{
			Name:        "grpc.io/server/received_bytes_per_rpc",
			Description: "Distribution of received bytes per RPC, by method.",
			TagKeys:     []tag.Key{appIDKey, KeyServerMethod},
			Measure:     g.serverReceivedBytes,
			Aggregation: defaultSizeDistribution,
		},
		{
			Name:        "grpc.io/server/sent_bytes_per_rpc",
			Description: "Distribution of total sent bytes per RPC, by method.",
			TagKeys:     []tag.Key{appIDKey, KeyServerMethod},
			Measure:     g.serverSentBytes,
			Aggregation: defaultSizeDistribution,
		},
		{
			Name:        "grpc.io/server/server_latency",
			Description: "Distribution of server latency in milliseconds, by method.",
			TagKeys:     []tag.Key{appIDKey, KeyServerMethod},
			Measure:     g.serverLatency,
			Aggregation: defaultLatencyDistribution,
		},
		{
			Name:        "grpc.io/server/completed_rpcs",
			Description: "Count of RPCs by method and status.",
			TagKeys:     []tag.Key{appIDKey, KeyServerMethod, KeyServerStatus},
			Measure:     g.serverLatency,
			Aggregation: view.Count(),
		},
		{
			Name:        "grpc.io/client/sent_bytes_per_rpc",
			Description: "Distribution of bytes sent per RPC, by method.",
			TagKeys:     []tag.Key{appIDKey, KeyClientMethod},
			Measure:     g.clientSentBytes,
			Aggregation: view.Count(),
		},

		{
			Name:        "grpc.io/client/received_bytes_per_rpc",
			Measure:     g.clientReceivedBytes,
			Aggregation: defaultSizeDistribution,
			Description: "Distribution of bytes received per RPC, by method.",
			TagKeys:     []tag.Key{appIDKey, KeyClientMethod},
		},
		{
			Name:        "grpc.io/client/completed_rpcs",
			Measure:     g.clientRoundtripLatency,
			Aggregation: defaultSizeDistribution,
			Description: "Count of RPCs by method and status.",
			TagKeys:     []tag.Key{appIDKey, KeyClientMethod, KeyClientStatus},
		},
	}

	return view.Register(views...)
}

func (g *grpcMetrics) IsEnabled() bool {
	return g.enabled
}

func (g *grpcMetrics) ServerRequestReceived(ctx context.Context, method string, contentSize int64) time.Time {
	if g.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, g.appID, KeyServerMethod, method),
			g.serverReceivedBytes.M(contentSize))
	}

	return time.Now()
}

func (g *grpcMetrics) ServerRequestSent(ctx context.Context, method, status string, contentSize int64, start time.Time) {
	if g.enabled {
		elapsed := float64(time.Since(start) / time.Millisecond)
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, g.appID, KeyServerMethod, method),
			g.serverSentBytes.M(contentSize))
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, g.appID, KeyServerMethod, method, KeyServerStatus, status),
			g.serverLatency.M(elapsed))
	}
}

func (g *grpcMetrics) ClientRequestSent(ctx context.Context, method string, contentSize int64) time.Time {
	if g.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, g.appID, KeyClientMethod, method),
			g.clientSentBytes.M(contentSize))
	}

	return time.Now()
}

func (g *grpcMetrics) ClientRequestRecieved(ctx context.Context, method, status string, contentSize int64, start time.Time) {
	if g.enabled {
		elapsed := float64(time.Since(start) / time.Millisecond)
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, g.appID, KeyClientMethod, method, KeyClientStatus, status),
			g.clientRoundtripLatency.M(elapsed))
		stats.RecordWithTags(
			ctx, diag_utils.WithTags(appIDKey, g.appID),
			g.clientReceivedBytes.M(contentSize))
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
		start := g.ClientRequestSent(ctx, method, int64(g.getPayloadSize(req)))
		err := invoker(ctx, method, req, reply, cc, opts...)
		size := 0
		if err == nil {
			size = g.getPayloadSize(reply)
		}
		g.ClientRequestRecieved(ctx, method, status.Code(err).String(), int64(size), start)
		return err
	}
}
