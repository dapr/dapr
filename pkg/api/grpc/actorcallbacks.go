/*
Copyright 2026 The Dapr Authors
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

package grpc

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dapr/dapr/pkg/actors/callbackstream"
	"github.com/dapr/dapr/pkg/actors/hostconfig"
	grpcchannel "github.com/dapr/dapr/pkg/channel/grpc"
	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/kit/concurrency"
)

// SubscribeActorEventsAlpha1 is the server-side endpoint of the
// app-initiated actor callback stream. The app dials daprd, opens this
// stream, sends a registration message advertising the actor types it
// hosts, and then exchanges invoke/reminder/timer/deactivate callback
// pairs with daprd. Apps using this RPC do not need to expose a server
// port for actor callbacks.
func (a *api) SubscribeActorEventsAlpha1(stream runtimev1pb.Dapr_SubscribeActorEventsAlpha1Server) error {
	mgr := a.actorCallbackStream()
	if mgr == nil {
		return status.Error(codes.FailedPrecondition, "actor callback streaming requires a gRPC app channel")
	}

	// Read the initial registration.
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	initial := first.GetInitialRequest()
	if initial == nil {
		return status.Error(codes.InvalidArgument, "first message must be SubscribeActorEventsRequestInitialAlpha1")
	}
	cfg := initialToAppConfig(initial)

	// Drive the lifecycle imperatively from the API handler, the same shape
	// as the subscriptions flow in pkg/api/grpc/subscribe.go. Register the
	// actor types with the runtime — an empty Entities list still
	// transitions the actor runtime out of INITIALIZING, mirroring the
	// HTTP /dapr/config 404 path — then register the stream connection
	// with the manager for Send/Deliver routing.
	if err := a.Actors().RegisterHosted(hostconfig.Config{
		EntityConfigs:           cfg.EntityConfigs,
		DrainRebalancedActors:   cfg.DrainRebalancedActors,
		DrainOngoingCallTimeout: cfg.DrainOngoingCallTimeout,
		HostedActorTypes:        cfg.Entities,
		DefaultIdleTimeout:      cfg.ActorIdleTimeout,
		Reentrancy:              cfg.Reentrancy,
		AppChannel:              a.channels.AppChannel(),
	}); err != nil {
		return status.Errorf(codes.Internal, "failed to register hosted actors: %v", err)
	}
	conn := mgr.Register(stream.Context(), cfg)
	// Cleanup runs as one deferred block so ordering is explicit: drop the
	// actor types from the runtime table first so daprd routes no further
	// new traffic to this connection, then close the connection to fail
	// any in-flight Sends with ErrDisconnected.
	defer func() {
		a.Actors().UnRegisterHosted(cfg.Entities...)
		mgr.Close(conn, nil)
	}()

	// Ack the registration.
	if err := stream.Send(&runtimev1pb.SubscribeActorEventsResponseAlpha1{
		ResponseType: &runtimev1pb.SubscribeActorEventsResponseAlpha1_InitialResponse{
			InitialResponse: &runtimev1pb.SubscribeActorEventsResponseInitialAlpha1{},
		},
	}); err != nil {
		return err
	}

	// Drive send and recv as siblings under a RunnerManager so cleanup is
	// explicit: whichever loop returns first causes the manager to cancel
	// the shared context, and the other loop unwinds promptly. This mirrors
	// the scheduler streamer pattern (pkg/runtime/scheduler/internal/cluster/streamer.go).
	return concurrency.NewRunnerManager(
		func(ctx context.Context) error { return recvLoop(ctx, stream, conn) },
		func(ctx context.Context) error { return sendLoop(ctx, stream, conn, a.closeCh) },
	).Run(stream.Context())
}

// recvLoop reads inbound messages from the stream and routes them to the
// pending caller. stream.Recv blocks until the underlying gRPC stream
// produces a message or is torn down, so the actual read runs on an inner
// goroutine — the outer loop selects on ctx so a sibling runner returning
// (sendLoop exit, a.closeCh) unwinds recv promptly. The inner goroutine
// then exits naturally when gRPC cancels stream.Context() after the
// handler returns.
func recvLoop(
	ctx context.Context,
	stream runtimev1pb.Dapr_SubscribeActorEventsAlpha1Server,
	conn *callbackstream.Connection,
) error {
	type recvResult struct {
		msg *runtimev1pb.SubscribeActorEventsRequestAlpha1
		err error
	}
	results := make(chan recvResult, 1)
	go func() {
		defer close(results)
		for {
			msg, err := stream.Recv()
			// Select on ctx so a cancelled outer loop doesn't deadlock the
			// inner goroutine on a full results buffer. If ctx is already
			// done, drop the message and exit — there's no consumer left.
			select {
			case results <- recvResult{msg, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case r, ok := <-results:
			if !ok {
				return nil
			}
			if r.err != nil {
				if errors.Is(r.err, io.EOF) {
					return nil
				}
				return r.err
			}
			conn.Deliver(r.msg)
		}
	}
}

// sendLoop drains the connection's outbox and writes each message to the
// stream. Exits on ctx cancel (sibling recv returned or RunnerManager is
// unwinding), conn.Done (manager closed the connection), or a.closeCh
// (api server shutdown). Outbox is intentionally never closed by the
// manager so we exit via conn.Done instead of ranging on it — see
// callbackstream.Connection.Outbox.
func sendLoop(
	ctx context.Context,
	stream runtimev1pb.Dapr_SubscribeActorEventsAlpha1Server,
	conn *callbackstream.Connection,
	closeCh <-chan struct{},
) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-conn.Done():
			return nil
		case <-closeCh:
			return nil
		case out := <-conn.Outbox:
			if err := stream.Send(out); err != nil {
				return err
			}
		}
	}
}

// actorCallbackStream returns the stream manager owned by the gRPC app
// channel, or nil if the channel is not gRPC-backed (e.g. HTTP app).
func (a *api) actorCallbackStream() *callbackstream.Manager {
	if a.channels == nil {
		return nil
	}
	ch := a.channels.AppChannel()
	if ch == nil {
		return nil
	}
	gc, ok := ch.(*grpcchannel.Channel)
	if !ok {
		return nil
	}
	return gc.ActorCallbackStream()
}

// initialToAppConfig translates the wire-level registration message into
// the internal ApplicationConfig shape used by the rest of the runtime.
func initialToAppConfig(req *runtimev1pb.SubscribeActorEventsRequestInitialAlpha1) *config.ApplicationConfig {
	cfg := &config.ApplicationConfig{
		Entities:                req.GetEntities(),
		ActorIdleTimeout:        durationToString(req.GetActorIdleTimeout()),
		DrainOngoingCallTimeout: durationToString(req.GetDrainOngoingCallTimeout()),
		Reentrancy:              reentrancyConfigFromProto(req.GetReentrancy()),
	}
	if req.DrainRebalancedActors != nil {
		v := req.GetDrainRebalancedActors()
		cfg.DrainRebalancedActors = &v
	}
	if entityCfgs := req.GetEntitiesConfig(); len(entityCfgs) > 0 {
		cfg.EntityConfigs = make([]config.EntityConfig, 0, len(entityCfgs))
		for _, ec := range entityCfgs {
			converted := config.EntityConfig{
				Entities:                ec.GetEntities(),
				ActorIdleTimeout:        durationToString(ec.GetActorIdleTimeout()),
				DrainOngoingCallTimeout: durationToString(ec.GetDrainOngoingCallTimeout()),
				Reentrancy:              reentrancyConfigFromProto(ec.GetReentrancy()),
			}
			if ec.DrainRebalancedActors != nil {
				v := ec.GetDrainRebalancedActors()
				converted.DrainRebalancedActors = &v
			}
			cfg.EntityConfigs = append(cfg.EntityConfigs, converted)
		}
	}
	return cfg
}

// durationToString converts an optional protobuf Duration to the
// duration-string form (e.g. "1h", "30s") that the internal
// ApplicationConfig carries. nil and zero values map to "" so the
// runtime falls back to its defaults.
func durationToString(d *durationpb.Duration) string {
	if d == nil {
		return ""
	}
	return d.AsDuration().String()
}

func reentrancyConfigFromProto(r *runtimev1pb.ActorReentrancyConfig) config.ReentrancyConfig {
	if r == nil {
		return config.ReentrancyConfig{}
	}
	out := config.ReentrancyConfig{Enabled: r.GetEnabled()}
	if r.MaxStackDepth != nil {
		v := int(r.GetMaxStackDepth())
		out.MaxStackDepth = &v
	}
	return out
}
