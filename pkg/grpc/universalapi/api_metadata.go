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

package universalapi

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/config/protocol"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

const daprRuntimeVersionKey = "daprRuntimeVersion"

func (a *UniversalAPI) GetMetadata(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.GetMetadataResponse, error) {
	// Extended metadata
	extendedMetadata := make(map[string]string, len(a.ExtendedMetadata)+1)
	a.extendedMetadataLock.RLock()
	for k, v := range a.ExtendedMetadata {
		extendedMetadata[k] = v
	}
	a.extendedMetadataLock.RUnlock()

	// This is deprecated, but we still need to support it for backward compatibility.
	extendedMetadata[daprRuntimeVersionKey] = buildinfo.Version()

	// Active actors count
	activeActorsCount := []*runtimev1pb.ActiveActorsCount{}
	if a.Actors != nil {
		activeActorsCount = a.Actors.GetActiveActorsCount(ctx)
	}

	// App connection information
	appConnectionProperties := &runtimev1pb.AppConnectionProperties{
		ChannelAddress: a.AppConnectionConfig.ChannelAddress,
		Port:           int32(a.AppConnectionConfig.Port),
		Protocol:       string(a.AppConnectionConfig.Protocol),
		MaxConcurrency: int32(a.AppConnectionConfig.MaxConcurrency),
	}

	if a.AppConnectionConfig.HealthCheck != nil {
		appConnectionProperties.Health = &runtimev1pb.AppConnectionHealthProperties{
			HealthProbeInterval: a.AppConnectionConfig.HealthCheck.ProbeInterval.String(),
			HealthProbeTimeout:  a.AppConnectionConfig.HealthCheck.ProbeTimeout.String(),
			HealthThreshold:     a.AppConnectionConfig.HealthCheck.Threshold,
		}

		// Health check path is not applicable for gRPC.
		if protocol.Protocol(appConnectionProperties.Protocol).IsHTTP() {
			appConnectionProperties.Health.HealthCheckPath = a.AppConnectionConfig.HealthCheckHTTPPath
		}
	}

	// Components
	components := a.CompStore.ListComponents()
	registeredComponents := make([]*runtimev1pb.RegisteredComponents, len(components))
	componentsCapabilities := a.GetComponentsCapabilitesFn()
	for i, comp := range components {
		registeredComponents[i] = &runtimev1pb.RegisteredComponents{
			Name:         comp.Name,
			Version:      comp.Spec.Version,
			Type:         comp.Spec.Type,
			Capabilities: metadataGetOrDefaultCapabilities(componentsCapabilities, comp.Name),
		}
	}

	// Subscriptions
	subscriptions := a.CompStore.ListSubscriptions()
	ps := make([]*runtimev1pb.PubsubSubscription, len(subscriptions))
	for i, s := range subscriptions {
		ps[i] = &runtimev1pb.PubsubSubscription{
			PubsubName:      s.PubsubName,
			Topic:           s.Topic,
			Metadata:        s.Metadata,
			DeadLetterTopic: s.DeadLetterTopic,
			Rules:           metadataConvertPubSubSubscriptionRules(s.Rules),
		}
	}

	// HTTP endpoints
	endpoints := a.CompStore.ListHTTPEndpoints()
	registeredHTTPEndpoints := make([]*runtimev1pb.MetadataHTTPEndpoint, len(endpoints))
	for i, e := range endpoints {
		registeredHTTPEndpoints[i] = &runtimev1pb.MetadataHTTPEndpoint{
			Name: e.Name,
		}
	}

	return &runtimev1pb.GetMetadataResponse{
		Id:                      a.AppID,
		ExtendedMetadata:        extendedMetadata,
		RegisteredComponents:    registeredComponents,
		ActiveActorsCount:       activeActorsCount,
		Subscriptions:           ps,
		HttpEndpoints:           registeredHTTPEndpoints,
		AppConnectionProperties: appConnectionProperties,
		RuntimeVersion:          buildinfo.Version(),
		EnabledFeatures:         a.GlobalConfig.EnabledFeatures(),
	}, nil
}

// SetMetadata Sets value in extended metadata of the sidecar.
func (a *UniversalAPI) SetMetadata(ctx context.Context, in *runtimev1pb.SetMetadataRequest) (*emptypb.Empty, error) {
	a.extendedMetadataLock.Lock()
	if a.ExtendedMetadata == nil {
		a.ExtendedMetadata = make(map[string]string)
	}
	a.ExtendedMetadata[in.Key] = in.Value
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
		Rules: make([]*runtimev1pb.PubsubSubscriptionRule, 0),
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, &runtimev1pb.PubsubSubscriptionRule{
			// TODO avoid using fmt.Sprintf
			Match: fmt.Sprintf("%s", r.Match),
			Path:  r.Path,
		})
	}
	return out
}
