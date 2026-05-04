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
	"errors"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/actors/callbackstream"
	"github.com/dapr/dapr/pkg/actors/hostconfig"
	grpcchannel "github.com/dapr/dapr/pkg/channel/grpc"
	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
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

	// Mirror the subscriptions flow (pkg/api/grpc/subscribe.go): drive the
	// lifecycle imperatively from the API handler rather than through a
	// callback. Register the actor types with the runtime (an empty
	// Entities list still transitions the actor runtime out of
	// INITIALIZING — matches the HTTP /dapr/config 404 path), then
	// register the stream connection with the manager for Send/Deliver
	// routing. Both are unregistered on exit.
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
	defer a.Actors().UnRegisterHosted(cfg.Entities...)
	conn := mgr.Register(stream.Context(), cfg)
	defer mgr.Close(conn, nil)

	// Ack the registration.
	if err := stream.Send(&runtimev1pb.SubscribeActorEventsResponseAlpha1{
		ResponseType: &runtimev1pb.SubscribeActorEventsResponseAlpha1_InitialResponse{
			InitialResponse: &runtimev1pb.SubscribeActorEventsResponseInitialAlpha1{},
		},
	}); err != nil {
		return err
	}

	errCh := make(chan error, 2)

	// Recv loop: route every inbound response to the pending caller.
	go func() {
		for {
			msg, recvErr := stream.Recv()
			if recvErr != nil {
				if errors.Is(recvErr, io.EOF) {
					errCh <- nil
					return
				}
				errCh <- recvErr
				return
			}
			conn.Deliver(msg)
		}
	}()

	// Send loop: drain the connection's outbox. Outbox is never closed by
	// the manager (senders can race with close), so exit on conn.Done()
	// instead of ranging on the channel.
	go func() {
		for {
			select {
			case <-conn.Done():
				errCh <- nil
				return
			case out := <-conn.Outbox:
				if sendErr := stream.Send(out); sendErr != nil {
					errCh <- sendErr
					return
				}
			}
		}
	}()

	select {
	case <-stream.Context().Done():
		return stream.Context().Err()
	case <-a.closeCh:
		return nil
	case err := <-errCh:
		return err
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
		ActorIdleTimeout:        req.GetActorIdleTimeout(),
		DrainOngoingCallTimeout: req.GetDrainOngoingCallTimeout(),
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
				ActorIdleTimeout:        ec.GetActorIdleTimeout(),
				DrainOngoingCallTimeout: ec.GetDrainOngoingCallTimeout(),
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
