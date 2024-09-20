/*
Copyright 2023 The Dapr Authors
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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// SubscribeTopicEvents is called by the Dapr runtime to ad hoc stream
// subscribe to topics. If gRPC API server closes, returns func early with nil
// to close stream.
func (a *api) SubscribeTopicEventsAlpha1(stream runtimev1pb.Dapr_SubscribeTopicEventsAlpha1Server) error {
	errCh := make(chan error, 2)
	subDone := make(chan struct{})
	a.wg.Add(2)

	go func() {
		defer a.wg.Done()
		select {
		case <-a.closeCh:
			errCh <- errors.New("api server closed")
		case <-subDone:
		}
	}()

	go func() {
		defer a.wg.Done()
		errCh <- a.streamSubscribe(stream, subDone)
	}()

	return <-errCh
}

func (a *api) streamSubscribe(stream runtimev1pb.Dapr_SubscribeTopicEventsAlpha1Server, subDone chan struct{}) error {
	defer close(subDone)

	ireq, err := stream.Recv()
	if err != nil {
		return err
	}

	req := ireq.GetInitialRequest()

	if req == nil {
		return errors.New("initial request is required")
	}

	if len(req.GetPubsubName()) == 0 {
		return errors.New("pubsubName is required")
	}

	if len(req.GetTopic()) == 0 {
		return errors.New("topic is required")
	}

	key := a.pubsubAdapterStreamer.StreamerKey(req.GetPubsubName(), req.GetTopic())
	err = a.Universal.CompStore().AddStreamSubscription(&subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: key},
		Spec: subapi.SubscriptionSpec{
			Pubsubname:      req.GetPubsubName(),
			Topic:           req.GetTopic(),
			Metadata:        req.GetMetadata(),
			DeadLetterTopic: req.GetDeadLetterTopic(),
			Routes:          subapi.Routes{Default: "/"},
		},
	})
	if err != nil {
		return err
	}

	if err = a.processor.Subscriber().StartStreamerSubscription(key); err != nil {
		a.Universal.CompStore().DeleteStreamSubscription(key)
		return err
	}

	defer func() {
		a.processor.Subscriber().StopStreamerSubscription(req.GetPubsubName(), key)
		a.Universal.CompStore().DeleteStreamSubscription(key)
	}()

	if err = stream.Send(&runtimev1pb.SubscribeTopicEventsResponseAlpha1{
		SubscribeTopicEventsResponseType: &runtimev1pb.SubscribeTopicEventsResponseAlpha1_InitialResponse{
			InitialResponse: new(runtimev1pb.SubscribeTopicEventsResponseInitialAlpha1),
		},
	}); err != nil {
		return err
	}

	return a.pubsubAdapterStreamer.Subscribe(stream, req)
}
