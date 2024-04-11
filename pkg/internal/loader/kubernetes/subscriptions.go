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

package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"

	grpcretry "github.com/grpc-ecosystem/go-grpc-middleware/retry"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/internal/loader"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

// subscriptions loads subscriptions from a Kubernetes environment.
type subscriptions struct {
	client    operatorv1pb.OperatorClient
	namespace string
	podName   string
}

// NewSubscriptions returns a new Kubernetes loader.
func NewSubscriptions(opts Options) loader.Loader[subapi.Subscription] {
	return &subscriptions{
		client:    opts.Client,
		namespace: opts.Namespace,
		podName:   opts.PodName,
	}
}

func (s *subscriptions) Load(ctx context.Context) ([]subapi.Subscription, error) {
	resp, err := s.client.ListSubscriptionsV2(ctx, &operatorv1pb.ListSubscriptionsRequest{
		Namespace: s.namespace,
		PodName:   s.podName,
	}, grpcretry.WithMax(operatorMaxRetries), grpcretry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		return nil, err
	}
	subs := resp.GetSubscriptions()

	subscriptions := make([]subapi.Subscription, len(subs))
	for i, s := range subs {
		var subscription subapi.Subscription
		if err := json.Unmarshal(s, &subscription); err != nil {
			return nil, fmt.Errorf("error deserializing subscription: %s", err)
		}

		subscriptions[i] = subscription
	}

	return subscriptions, nil
}
