// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"

	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"go.opencensus.io/plugin/ocgrpc"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/trace"
	"google.golang.org/grpc/stats"
)

// Dapr wraps ocgrpc plugin to emit metrics:
// https://github.com/census-instrumentation/opencensus-go/tree/master/plugin/ocgrpc

// TODO: by default, ocgrpc emits both trace and metrics. This implementation turns off
// trace because we have our own trace middleware. Later, we need to consider to turn on trace
// to leverage the popular existing implementation instead of implementing our own.

// gRPCServerHandler is the wrapper of grpc server plugin of opencensus
// to add custom tag key and disable tracing
type gRPCServerHandler struct {
	ocHandler *ocgrpc.ServerHandler
	appID     string
	enabled   bool
}

func (s *gRPCServerHandler) HandleConn(ctx context.Context, cs stats.ConnStats) {
}

func (s *gRPCServerHandler) TagConn(ctx context.Context, cti *stats.ConnTagInfo) context.Context {
	return ctx
}

func (s *gRPCServerHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	if s.enabled {
		s.ocHandler.HandleRPC(diag_utils.AddTagKeyToCtx(ctx, appIDKey, s.appID), rs)
	}
}

func (s *gRPCServerHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	if s.enabled {
		return s.ocHandler.TagRPC(diag_utils.AddTagKeyToCtx(ctx, appIDKey, s.appID), rti)
	}
	return ctx
}

// gRPCClientHandler is the wrapper of grpc client plugin of opencensus
// to add custom tag key and disable tracing.
type gRPCClientHandler struct {
	ocHandler *ocgrpc.ClientHandler
	appID     string
	enabled   bool
}

func (c *gRPCClientHandler) HandleConn(ctx context.Context, cs stats.ConnStats) {}

func (c *gRPCClientHandler) TagConn(ctx context.Context, cti *stats.ConnTagInfo) context.Context {
	return ctx
}

func (c *gRPCClientHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	if c.enabled {
		c.ocHandler.HandleRPC(diag_utils.AddTagKeyToCtx(ctx, appIDKey, c.appID), rs)
	}
}

func (c *gRPCClientHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	if c.enabled {
		return c.ocHandler.TagRPC(diag_utils.AddTagKeyToCtx(ctx, appIDKey, c.appID), rti)
	}
	return ctx
}

// grpcMetrics holds gRPC server and client stats handlers
type grpcMetrics struct {
	ServerStatsHandler stats.Handler
	ClientStatsHandler stats.Handler
}

// Init initializes metric view and creates gRPC server/client handlers
func (g *grpcMetrics) Init(appID string) error {
	// Register default grpc server views
	if err := view.Register(diag_utils.AddNewTagKey(ocgrpc.DefaultServerViews, &appIDKey)...); err != nil {
		return err
	}

	if err := view.Register(diag_utils.AddNewTagKey(ocgrpc.DefaultClientViews, &appIDKey)...); err != nil {
		return err
	}

	// noTracing turns off trace since ocgrpc plugin emits both tracing and metrics.
	var noTracing = trace.StartOptions{Sampler: trace.NeverSample()}
	g.ServerStatsHandler = &gRPCServerHandler{
		ocHandler: &ocgrpc.ServerHandler{StartOptions: noTracing},
		appID:     appID,
		enabled:   true,
	}
	g.ClientStatsHandler = &gRPCClientHandler{
		ocHandler: &ocgrpc.ClientHandler{StartOptions: noTracing},
		appID:     appID,
		enabled:   true,
	}

	return nil
}

// newGRPCMetrics creates gRPCMetrics instance
func newGRPCMetrics() grpcMetrics {
	return grpcMetrics{}
}
