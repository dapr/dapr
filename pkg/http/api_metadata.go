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
	"github.com/valyala/fasthttp"
	"google.golang.org/protobuf/types/known/emptypb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *api) constructMetadataEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "metadata",
			Version: apiVersionV1,
			Handler: a.onGetMetadata(),
		},
		{
			Methods: []string{fasthttp.MethodPut},
			Route:   "metadata/{key}",
			Version: apiVersionV1,
			Handler: a.onPutMetadata(),
		},
	}
}

func (a *api) onGetMetadata() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.GetMetadata,
		UniversalFastHTTPHandlerOpts[*emptypb.Empty, *runtimev1pb.GetMetadataResponse]{
			OutModifier: func(out *runtimev1pb.GetMetadataResponse) (any, error) {
				// In the protos, the property subscriptions[*].rules is serialized as subscriptions[*].rules.rules
				// To maintain backwards-compatibility, we need to copy into a custom struct and marshal that instead
				res := &metadataResponse{
					ID:       out.Id,
					Extended: out.ExtendedMetadata,
					// We can embed the proto object directly only for as long as the protojson key is == json key
					ActiveActorsCount:    out.ActiveActorsCount,
					RegisteredComponents: out.RegisteredComponents,
					HTTPEndpoints:        out.HttpEndpoints,
					RuntimeVersion:       out.RuntimeVersion,
					EnabledFeatures:      out.EnabledFeatures,
				}

				// Copy the app connection properties into a custom struct
				// See https://github.com/golang/protobuf/issues/256
				res.AppConnectionProperties = metadataResponseAppConnectionProperties{
					Port:           out.AppConnectionProperties.Port,
					Protocol:       out.AppConnectionProperties.Protocol,
					ChannelAddress: out.AppConnectionProperties.ChannelAddress,
					MaxConcurrency: out.AppConnectionProperties.MaxConcurrency,
				}
				if out.AppConnectionProperties.Health != nil {
					res.AppConnectionProperties.Health = &metadataResponseAppConnectionHealthProperties{
						HealthCheckPath:     out.AppConnectionProperties.Health.HealthCheckPath,
						HealthProbeInterval: out.AppConnectionProperties.Health.HealthProbeInterval,
						HealthProbeTimeout:  out.AppConnectionProperties.Health.HealthProbeTimeout,
						HealthThreshold:     out.AppConnectionProperties.Health.HealthThreshold,
					}
				}

				// Copy the subscriptions into a custom struct
				if len(out.Subscriptions) > 0 {
					subs := make([]metadataResponsePubsubSubscription, len(out.Subscriptions))
					for i, v := range out.Subscriptions {
						subs[i] = metadataResponsePubsubSubscription{
							PubsubName:      v.PubsubName,
							Topic:           v.Topic,
							Metadata:        v.Metadata,
							DeadLetterTopic: v.DeadLetterTopic,
						}

						if v.Rules != nil && len(v.Rules.Rules) > 0 {
							subs[i].Rules = make([]metadataResponsePubsubSubscriptionRule, len(v.Rules.Rules))
							for j, r := range v.Rules.Rules {
								subs[i].Rules[j] = metadataResponsePubsubSubscriptionRule{
									Match: r.Match,
									Path:  r.Path,
								}
							}
						}
					}
					res.Subscriptions = subs
				}

				return res, nil
			},
		},
	)
}

func (a *api) onPutMetadata() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.SetMetadata,
		UniversalFastHTTPHandlerOpts[*runtimev1pb.SetMetadataRequest, *emptypb.Empty]{
			SkipInputBody: true,
			InModifier: func(reqCtx *fasthttp.RequestCtx, in *runtimev1pb.SetMetadataRequest) (*runtimev1pb.SetMetadataRequest, error) {
				in.Key = reqCtx.UserValue("key").(string)
				in.Value = string(reqCtx.Request.Body())
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
}

type metadataResponsePubsubSubscription struct {
	PubsubName      string                                   `json:"pubsubname"`
	Topic           string                                   `json:"topic"`
	Metadata        map[string]string                        `json:"metadata,omitempty"`
	Rules           []metadataResponsePubsubSubscriptionRule `json:"rules,omitempty"`
	DeadLetterTopic string                                   `json:"deadLetterTopic"`
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
