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

package http

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

var endpointGroupMetadataV1 = &endpoints.EndpointGroup{
	Name:                 endpoints.EndpointGroupMetadata,
	Version:              endpoints.EndpointGroupVersion1,
	AppendSpanAttributes: nil, // TODO
}

func (a *api) constructMetadataEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{http.MethodGet},
			Route:   "metadata",
			Version: apiVersionV1,
			Group:   endpointGroupMetadataV1,
			Handler: a.onGetMetadata(),
			Settings: endpoints.EndpointSettings{
				Name: "GetMetadata",
			},
		},
		{
			Methods: []string{http.MethodPut},
			Route:   "metadata/{key}",
			Version: apiVersionV1,
			Group:   endpointGroupMetadataV1,
			Handler: a.onPutMetadata(),
			Settings: endpoints.EndpointSettings{
				Name: "PutMetadata",
			},
		},
	}
}

func (a *api) onGetMetadata() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.GetMetadata,
		UniversalHTTPHandlerOpts[*runtimev1pb.GetMetadataRequest, *runtimev1pb.GetMetadataResponse]{
			OutModifier: func(out *runtimev1pb.GetMetadataResponse) (any, error) {
				// In the protos, the property subscriptions[*].rules is serialized as subscriptions[*].rules.rules
				// To maintain backwards-compatibility, we need to copy into a custom struct and marshal that instead
				res := &metadataResponse{
					ID:       out.GetId(),
					Extended: out.GetExtendedMetadata(),
					// We can embed the proto object directly only for as long as the protojson key is == json key
					ActiveActorsCount:    out.GetActiveActorsCount(), //nolint:staticcheck
					RegisteredComponents: out.GetRegisteredComponents(),
					HTTPEndpoints:        out.GetHttpEndpoints(),
					RuntimeVersion:       out.GetRuntimeVersion(),
					EnabledFeatures:      out.GetEnabledFeatures(),
					//nolint:protogetter
					Scheduler: out.Scheduler,
				}

				// Copy the app connection properties into a custom struct
				// See https://github.com/golang/protobuf/issues/256
				res.AppConnectionProperties = metadataResponseAppConnectionProperties{
					Port:           out.GetAppConnectionProperties().GetPort(),
					Protocol:       out.GetAppConnectionProperties().GetProtocol(),
					ChannelAddress: out.GetAppConnectionProperties().GetChannelAddress(),
					MaxConcurrency: out.GetAppConnectionProperties().GetMaxConcurrency(),
				}
				if out.GetAppConnectionProperties().GetHealth() != nil {
					res.AppConnectionProperties.Health = &metadataResponseAppConnectionHealthProperties{
						HealthCheckPath:     out.GetAppConnectionProperties().GetHealth().GetHealthCheckPath(),
						HealthProbeInterval: out.GetAppConnectionProperties().GetHealth().GetHealthProbeInterval(),
						HealthProbeTimeout:  out.GetAppConnectionProperties().GetHealth().GetHealthProbeTimeout(),
						HealthThreshold:     out.GetAppConnectionProperties().GetHealth().GetHealthThreshold(),
					}
				}

				// Copy the subscriptions into a custom struct
				if len(out.GetSubscriptions()) > 0 {
					subs := make([]metadataResponsePubsubSubscription, len(out.GetSubscriptions()))
					for i, v := range out.GetSubscriptions() {
						// TODO: switch to runtimev1pb.PubsubSubscription. Rules becomes Rules.Rules
						subs[i] = metadataResponsePubsubSubscription{
							PubsubName:      v.GetPubsubName(),
							Topic:           v.GetTopic(),
							Metadata:        v.GetMetadata(),
							DeadLetterTopic: v.GetDeadLetterTopic(),
							Type:            v.GetType().String(),
						}

						if v.GetRules() != nil && len(v.GetRules().GetRules()) > 0 {
							subs[i].Rules = make([]metadataResponsePubsubSubscriptionRule, len(v.GetRules().GetRules()))
							for j, r := range v.GetRules().GetRules() {
								subs[i].Rules[j] = metadataResponsePubsubSubscriptionRule{
									Match: r.GetMatch(),
									Path:  r.GetPath(),
								}
							}
						}
					}
					res.Subscriptions = subs
				}

				// Actor runtime
				// We need to include the status as string
				actorRuntime := out.GetActorRuntime()
				res.ActorRuntime = metadataActorRuntime{
					Status:       actorRuntime.GetRuntimeStatus().String(),
					ActiveActors: actorRuntime.GetActiveActors(),
					HostReady:    actorRuntime.GetHostReady(),
					Placement:    actorRuntime.GetPlacement(),
				}

				if out.GetWorkflows() != nil {
					res.Workflows = metadataWorkflows{
						ConnectedWorkers: out.GetWorkflows().GetConnectedWorkers(),
					}
				}

				return res, nil
			},
		},
	)
}

func (a *api) onPutMetadata() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.SetMetadata,
		UniversalHTTPHandlerOpts[*runtimev1pb.SetMetadataRequest, *emptypb.Empty]{
			SkipInputBody: true,
			InModifier: func(r *http.Request, in *runtimev1pb.SetMetadataRequest) (*runtimev1pb.SetMetadataRequest, error) {
				in.Key = chi.URLParam(r, "key")

				body, err := io.ReadAll(r.Body)
				if err != nil {
					return nil, messages.ErrBodyRead.WithFormat(err)
				}
				in.Value = string(body)

				return in, nil
			},
			OutModifier: func(out *emptypb.Empty) (any, error) {
				// Nullify the response so status code is 204
				return nil, nil
			},
		},
	)
}

type metadataResponse struct {
	ID                      string                                  `json:"id,omitempty"`
	RuntimeVersion          string                                  `json:"runtimeVersion,omitempty"`
	EnabledFeatures         []string                                `json:"enabledFeatures,omitempty"`
	ActiveActorsCount       []*runtimev1pb.ActiveActorsCount        `json:"actors,omitempty"`
	RegisteredComponents    []*runtimev1pb.RegisteredComponents     `json:"components,omitempty"`
	Extended                map[string]string                       `json:"extended,omitempty"`
	Subscriptions           []metadataResponsePubsubSubscription    `json:"subscriptions,omitempty"`
	HTTPEndpoints           []*runtimev1pb.MetadataHTTPEndpoint     `json:"httpEndpoints,omitempty"`
	AppConnectionProperties metadataResponseAppConnectionProperties `json:"appConnectionProperties,omitempty"`
	ActorRuntime            metadataActorRuntime                    `json:"actorRuntime,omitempty"`
	Scheduler               *runtimev1pb.MetadataScheduler          `json:"scheduler,omitempty"`
	Workflows               metadataWorkflows                       `json:"workflows,omitempty"`
}

type metadataWorkflows struct {
	ConnectedWorkers int32 `json:"connectedWorkers,omitempty"`
}

type metadataActorRuntime struct {
	Status       string                           `json:"runtimeStatus"`
	ActiveActors []*runtimev1pb.ActiveActorsCount `json:"activeActors,omitempty"`
	HostReady    bool                             `json:"hostReady"`
	Placement    string                           `json:"placement,omitempty"`
}

type metadataResponsePubsubSubscription struct {
	PubsubName      string                                   `json:"pubsubname"`
	Topic           string                                   `json:"topic"`
	Metadata        map[string]string                        `json:"metadata,omitempty"`
	Rules           []metadataResponsePubsubSubscriptionRule `json:"rules,omitempty"`
	DeadLetterTopic string                                   `json:"deadLetterTopic"`
	Type            string                                   `json:"type"`
}

type metadataResponsePubsubSubscriptionRule struct {
	Match string `json:"match,omitempty"`
	Path  string `json:"path,omitempty"`
}

type metadataResponseAppConnectionProperties struct {
	Port           int32                                          `json:"port,omitempty"`
	Protocol       string                                         `json:"protocol,omitempty"`
	ChannelAddress string                                         `json:"channelAddress,omitempty"`
	MaxConcurrency int32                                          `json:"maxConcurrency,omitempty"`
	Health         *metadataResponseAppConnectionHealthProperties `json:"health,omitempty"`
}

type metadataResponseAppConnectionHealthProperties struct {
	HealthCheckPath     string `json:"healthCheckPath,omitempty"`
	HealthProbeInterval string `json:"healthProbeInterval,omitempty"`
	HealthProbeTimeout  string `json:"healthProbeTimeout,omitempty"`
	HealthThreshold     int32  `json:"healthThreshold,omitempty"`
}
