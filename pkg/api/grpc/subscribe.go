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

	a.wg.Add(2)
	go func() {
		defer a.wg.Done()
		select {
		case <-stream.Context().Done():
			errCh <- stream.Context().Err()
		case <-a.closeCh:
			errCh <- errors.New("api server closed")
		}
	}()

	var ireq *runtimev1pb.SubscribeTopicEventsRequestAlpha1
	var err error
	go func() {
		defer a.wg.Done()
		ireq, err = stream.Recv()
		errCh <- err
	}()

	if cerr := <-errCh; cerr != nil {
		return cerr
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
	sub := &subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: key},
		Spec: subapi.SubscriptionSpec{
			Pubsubname:      req.GetPubsubName(),
			Topic:           req.GetTopic(),
			Metadata:        req.GetMetadata(),
			DeadLetterTopic: req.GetDeadLetterTopic(),
			Routes:          subapi.Routes{Default: "/"},
		},
	}
	connectionID := a.pubsubAdapterStreamer.NextIndex()
	err = a.Universal.CompStore().AddStreamSubscription(sub, connectionID)
	if err != nil {
		return err
	}

	if err = a.processor.Subscriber().StartStreamerSubscription(sub, connectionID); err != nil {
		a.Universal.CompStore().DeleteStreamSubscription(sub)
		return err
	}

	defer func() {
		a.processor.Subscriber().StopStreamerSubscription(sub, connectionID)
		a.Universal.CompStore().DeleteStreamSubscription(sub)
	}()

	if err = stream.Send(&runtimev1pb.SubscribeTopicEventsResponseAlpha1{
		SubscribeTopicEventsResponseType: &runtimev1pb.SubscribeTopicEventsResponseAlpha1_InitialResponse{
			InitialResponse: new(runtimev1pb.SubscribeTopicEventsResponseInitialAlpha1),
		},
	}); err != nil {
		return err
	}

	return a.pubsubAdapterStreamer.Subscribe(stream, req, connectionID)
}
