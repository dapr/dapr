// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"

	"go.opencensus.io/plugin/ocgrpc"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"go.opencensus.io/trace"
	"google.golang.org/grpc/stats"
)

func addAppIDToCtx(ctx context.Context, appID string) context.Context {
	// return if appID is not given
	if appID == "" {
		return ctx
	}

	newCtx, err := tag.New(ctx, tag.Upsert(appIDKey, appID))
	if err != nil {
		// return original if adding tagkey is failed.
		return ctx
	}

	return newCtx
}

// gRPCServerHandler is the wrapper of grpc server plugin of opencensus
// to add custom tag key and disable tracing
// https://github.com/census-instrumentation/opencensus-go/tree/master/plugin/ocgrpc
type gRPCServerHandler struct {
	ocHandler *ocgrpc.ServerHandler
	appID     string
}

func (s *gRPCServerHandler) HandleConn(ctx context.Context, cs stats.ConnStats) {
}

func (s *gRPCServerHandler) TagConn(ctx context.Context, cti *stats.ConnTagInfo) context.Context {
	return ctx
}

func (s *gRPCServerHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	s.ocHandler.HandleRPC(addAppIDToCtx(ctx, s.appID), rs)
}

func (s *gRPCServerHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	return s.ocHandler.TagRPC(addAppIDToCtx(ctx, s.appID), rti)
}

// gRPCClientHandler is the wrapper of grpc client plugin of opencensus
// to add custom tag key and disable tracing
type gRPCClientHandler struct {
	ocHandler *ocgrpc.ClientHandler
	appID     string
}

func (c *gRPCClientHandler) HandleConn(ctx context.Context, cs stats.ConnStats) {}

func (c *gRPCClientHandler) TagConn(ctx context.Context, cti *stats.ConnTagInfo) context.Context {
	return ctx
}

func (c *gRPCClientHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	c.ocHandler.HandleRPC(addAppIDToCtx(ctx, c.appID), rs)
}

func (c *gRPCClientHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	return c.ocHandler.TagRPC(addAppIDToCtx(ctx, c.appID), rti)
}

// grpcMetrics holds gRPC server and client stats handlers
type grpcMetrics struct {
	ServerStatsHandler stats.Handler
	ClientStatsHandler stats.Handler
}

func (g *grpcMetrics) addNewTagKey(views []*view.View, key *tag.Key) []*view.View {
	for _, v := range views {
		v.TagKeys = append(v.TagKeys, *key)
	}

	return views
}

// Init initializes metric view and creates gRPC server/client handlers
func (g *grpcMetrics) Init(appID string) error {
	// Register default grpc server views
	if err := view.Register(g.addNewTagKey(ocgrpc.DefaultServerViews, &appIDKey)...); err != nil {
		return err
	}

	if err := view.Register(g.addNewTagKey(ocgrpc.DefaultClientViews, &appIDKey)...); err != nil {
		return err
	}

	// noTracing turns off trace since ocgrpc plugin emits both tracing and metrics.
	var noTracing = trace.StartOptions{Sampler: trace.NeverSample()}
	g.ServerStatsHandler = &gRPCServerHandler{
		ocHandler: &ocgrpc.ServerHandler{StartOptions: noTracing},
		appID:     appID,
	}
	g.ClientStatsHandler = &gRPCClientHandler{
		ocHandler: &ocgrpc.ClientHandler{StartOptions: noTracing},
		appID:     appID,
	}

	return nil
}

// newGRPCMetrics creates gRPCMetrics instance
func newGRPCMetrics() grpcMetrics {
	return grpcMetrics{}
}
