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
	"bytes"
	"encoding/json"

	"github.com/valyala/fasthttp"
	"google.golang.org/protobuf/encoding/protojson"
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
				// To maintain backwards-compatibility, we need to do this "hacky" thing
				// First, remove the subscriptions from the proto before encoding it
				subsProto := out.Subscriptions
				out.Subscriptions = nil
				enc, err := protojson.Marshal(out)
				if err != nil {
					return nil, err
				}

				// Now, marshal the subscriptions in a custom struct
				if len(subsProto) > 0 {
					subs := make([]pubsubSubscription, len(subsProto))
					for i, v := range subsProto {
						subs[i] = pubsubSubscription{
							PubsubName:      v.PubsubName,
							Topic:           v.Topic,
							Metadata:        v.Metadata,
							DeadLetterTopic: v.DeadLetterTopic,
						}

						if v.Rules != nil && len(v.Rules.Rules) > 0 {
							subs[i].Rules = make([]pubsubSubscriptionRule, len(v.Rules.Rules))
							for j, r := range v.Rules.Rules {
								subs[i].Rules[j] = pubsubSubscriptionRule{
									Match: r.Match,
									Path:  r.Path,
								}
							}
						}
					}

					// Do the marshalling
					subsEnc, err := json.Marshal(subs)
					if err != nil {
						return nil, err
					}

					// And "inject" in the JSON
					const token = `,"subscriptions":`
					buf := bytes.NewBuffer(enc[0 : len(enc)-1])
					buf.Grow(len(subsEnc) + len(token) + 1)
					buf.WriteString(token)
					buf.Write(subsEnc)
					buf.WriteByte(byte('}'))
					enc = buf.Bytes()
				}

				return &UniversalHTTPRawResponse{
					Body:        enc,
					ContentType: jsonContentTypeHeader,
				}, nil
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

type pubsubSubscription struct {
	PubsubName      string                   `json:"pubsubname"`
	Topic           string                   `json:"topic"`
	Metadata        map[string]string        `json:"metadata,omitempty"`
	Rules           []pubsubSubscriptionRule `json:"rules,omitempty"`
	DeadLetterTopic string                   `json:"deadLetterTopic"`
}

type pubsubSubscriptionRule struct {
	Match string `json:"match,omitempty"`
	Path  string `json:"path,omitempty"`
}
