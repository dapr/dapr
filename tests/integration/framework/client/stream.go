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

package client

import (
	"context"
	"fmt"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// SubscribeStream opens a streaming subscription to pubsubName/topic and
// returns it once daprd sent the initial response, which tells the client
// the subscription is ready. It returns errors rather than failing a test,
// so worker goroutines can use it.
func SubscribeStream(ctx context.Context, dapr rtv1.DaprClient, pubsubName, topic string) (rtv1.Dapr_SubscribeTopicEventsAlpha1Client, error) {
	stream, err := dapr.SubscribeTopicEventsAlpha1(ctx)
	if err != nil {
		return nil, err
	}
	if err = stream.Send(&rtv1.SubscribeTopicEventsRequestAlpha1{
		SubscribeTopicEventsRequestType: &rtv1.SubscribeTopicEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeTopicEventsRequestInitialAlpha1{PubsubName: pubsubName, Topic: topic},
		},
	}); err != nil {
		return nil, err
	}
	resp, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if resp.GetInitialResponse() == nil {
		return nil, fmt.Errorf("first response on %s/%s is not the initial response: %v", pubsubName, topic, resp)
	}
	return stream, nil
}
