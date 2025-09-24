/*
Copyright 2022 The Dapr Authors
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

package universal

import (
	"context"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/config/protocol"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

const daprRuntimeVersionKey = "daprRuntimeVersion"

func (a *Universal) GetMetadata(ctx context.Context, in *runtimev1pb.GetMetadataRequest) (*runtimev1pb.GetMetadataResponse, error) {
	// Extended metadata
	extendedMetadata := make(map[string]string, len(a.extendedMetadata)+1)
	a.extendedMetadataLock.RLock()
	for k, v := range a.extendedMetadata {
		extendedMetadata[k] = v
	}
	a.extendedMetadataLock.RUnlock()

	// This is deprecated, but we still need to support it for backward compatibility.
	extendedMetadata[daprRuntimeVersionKey] = buildinfo.Version()

	actorRuntime := a.actors.RuntimeStatus()
	workflowsMetadata := a.workflowEngine.RuntimeMetadata()

	// App connection information
	appConnectionProperties := &runtimev1pb.AppConnectionProperties{
		ChannelAddress: a.appConnectionConfig.ChannelAddress,
		// TODO: Update Ports and MaxConcurrency in runtime to use uint32
		//nolint:gosec
		Port:     int32(a.appConnectionConfig.Port),
		Protocol: string(a.appConnectionConfig.Protocol),
		//nolint:gosec
		MaxConcurrency: int32(a.appConnectionConfig.MaxConcurrency),
	}

	if a.appConnectionConfig.HealthCheck != nil {
		appConnectionProperties.Health = &runtimev1pb.AppConnectionHealthProperties{
			HealthProbeInterval: a.appConnectionConfig.HealthCheck.ProbeInterval.String(),
			HealthProbeTimeout:  a.appConnectionConfig.HealthCheck.ProbeTimeout.String(),
			HealthThreshold:     a.appConnectionConfig.HealthCheck.Threshold,
		}

		// Health check path is not applicable for gRPC.
		if protocol.Protocol(appConnectionProperties.GetProtocol()).IsHTTP() {
			appConnectionProperties.Health.HealthCheckPath = a.appConnectionConfig.HealthCheckHTTPPath
		}
	}

	// Components
	components := a.compStore.ListComponents()
	registeredComponents := make([]*runtimev1pb.RegisteredComponents, len(components))
	componentsCapabilities := a.getComponentsCapabilitiesFn()
	for i, comp := range components {
		registeredComponents[i] = &runtimev1pb.RegisteredComponents{
			Name:         comp.Name,
			Version:      comp.Spec.Version,
			Type:         comp.Spec.Type,
			Capabilities: metadataGetOrDefaultCapabilities(componentsCapabilities, comp.Name),
		}
	}

	// Subscriptions
	subscriptions := a.compStore.ListTypedSubscriptions()
	ps := make([]*runtimev1pb.PubsubSubscription, len(subscriptions))
	for i, s := range subscriptions {
		ps[i] = &runtimev1pb.PubsubSubscription{
			PubsubName:      s.PubsubName,
			Topic:           s.Topic,
			Metadata:        s.Metadata,
			DeadLetterTopic: s.DeadLetterTopic,
			Rules:           metadataConvertPubSubSubscriptionRules(s.Rules),
			Type:            s.Type,
		}
	}

	// HTTP endpoints
	endpoints := a.compStore.ListHTTPEndpoints()
	registeredHTTPEndpoints := make([]*runtimev1pb.MetadataHTTPEndpoint, len(endpoints))
	for i, e := range endpoints {
		registeredHTTPEndpoints[i] = &runtimev1pb.MetadataHTTPEndpoint{
			Name: e.Name,
		}
	}

	var sched *runtimev1pb.MetadataScheduler
	if a.scheduler != nil {
		if addr := a.scheduler.Addresses(); len(addr) > 0 {
			sched = &runtimev1pb.MetadataScheduler{
				ConnectedAddresses: addr,
			}
		}
	}

	return &runtimev1pb.GetMetadataResponse{
		Id:                      a.appID,
		ExtendedMetadata:        extendedMetadata,
		RegisteredComponents:    registeredComponents,
		ActiveActorsCount:       actorRuntime.GetActiveActors(), // Alias for backwards-compatibility
		Subscriptions:           ps,
		HttpEndpoints:           registeredHTTPEndpoints,
		AppConnectionProperties: appConnectionProperties,
		RuntimeVersion:          buildinfo.Version(),
		EnabledFeatures:         a.globalConfig.EnabledFeatures(),
		ActorRuntime:            actorRuntime,
		Scheduler:               sched,
		Workflows:               workflowsMetadata,
	}, nil
}

// SetMetadata Sets value in extended metadata of the sidecar.
func (a *Universal) SetMetadata(ctx context.Context, in *runtimev1pb.SetMetadataRequest) (*emptypb.Empty, error) {
	// Nop if the key is empty
	if in.GetKey() == "" {
		return &emptypb.Empty{}, nil
	}

	a.extendedMetadataLock.Lock()
	if a.extendedMetadata == nil {
		a.extendedMetadata = make(map[string]string)
	}
	a.extendedMetadata[in.GetKey()] = in.GetValue()
	a.extendedMetadataLock.Unlock()

	return &emptypb.Empty{}, nil
}

func metadataGetOrDefaultCapabilities(dict map[string][]string, key string) []string {
	if val, ok := dict[key]; ok {
		return val
	}
	return make([]string, 0)
}

func metadataConvertPubSubSubscriptionRules(rules []*runtimePubsub.Rule) *runtimev1pb.PubsubSubscriptionRules {
	out := &runtimev1pb.PubsubSubscriptionRules{
		Rules: make([]*runtimev1pb.PubsubSubscriptionRule, len(rules)),
	}
	for i, r := range rules {
		out.Rules[i] = &runtimev1pb.PubsubSubscriptionRule{
			Path: r.Path,
		}
		if r.Match != nil {
			out.Rules[i].Match = r.Match.String()
		}
	}
	return out
}
