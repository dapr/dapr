/*
Copyright 2024 The Dapr Authors
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

package operator

import (
	"context"
	"encoding/json"
	"fmt"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
)

type subscriptions struct {
	operatorpb.Operator_SubscriptionUpdateClient
}

// The go linter does not yet understand that these functions are being used by
// the generic operator.
//
//nolint:unused
func (s *subscriptions) list(ctx context.Context, opclient operatorpb.OperatorClient, ns, podName string) ([][]byte, error) {
	resp, err := opclient.ListSubscriptionsV2(ctx, &operatorpb.ListSubscriptionsRequest{
		Namespace: ns,
		PodName:   podName,
	})
	if err != nil {
		return nil, err
	}

	return resp.GetSubscriptions(), nil
}

//nolint:unused
func (s *subscriptions) close() error {
	if s.Operator_SubscriptionUpdateClient != nil {
		return s.Operator_SubscriptionUpdateClient.CloseSend()
	}
	return nil
}

//nolint:unused
func (s *subscriptions) recv() (*loader.Event[subapi.Subscription], error) {
	event, err := s.Operator_SubscriptionUpdateClient.Recv()
	if err != nil {
		return nil, err
	}

	var subscription subapi.Subscription
	if err := json.Unmarshal(event.GetSubscription(), &subscription); err != nil {
		return nil, fmt.Errorf("failed to deserializing subscription: %w", err)
	}

	return &loader.Event[subapi.Subscription]{
		Resource: subscription,
		Type:     event.GetType(),
	}, nil
}

//nolint:unused
func (s *subscriptions) establish(ctx context.Context, opclient operatorpb.OperatorClient, ns, podName string) error {
	stream, err := opclient.SubscriptionUpdate(ctx, &operatorpb.SubscriptionUpdateRequest{
		Namespace: ns,
		PodName:   podName,
	})
	if err != nil {
		return err
	}

	s.Operator_SubscriptionUpdateClient = stream
	return nil
}
