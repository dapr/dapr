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

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/emptypb"
	"sigs.k8s.io/controller-runtime/pkg/client"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/operator/api/authz"
	loopsclient "github.com/dapr/dapr/pkg/operator/api/loops/client"
	"github.com/dapr/dapr/pkg/operator/api/loops/sender"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

// ListSubscriptions returns a list of Dapr pub/sub subscriptions.
func (a *apiServer) ListSubscriptions(ctx context.Context, in *emptypb.Empty) (*operatorv1pb.ListSubscriptionsResponse, error) {
	return a.ListSubscriptionsV2(ctx, &operatorv1pb.ListSubscriptionsRequest{})
}

// ListSubscriptionsV2 returns a list of Dapr pub/sub subscriptions.
func (a *apiServer) ListSubscriptionsV2(ctx context.Context, in *operatorv1pb.ListSubscriptionsRequest) (*operatorv1pb.ListSubscriptionsResponse, error) {
	if _, err := authz.Request(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	resp := &operatorv1pb.ListSubscriptionsResponse{
		Subscriptions: [][]byte{},
	}

	// Only the latest/storage version needs to be returned.
	var subsV2alpha1 subapi.SubscriptionList
	if err := a.Client.List(ctx, &subsV2alpha1, &client.ListOptions{
		Namespace: in.GetNamespace(),
	}); err != nil {
		return nil, fmt.Errorf("error getting subscriptions: %w", err)
	}
	for i := range subsV2alpha1.Items {
		s := subsV2alpha1.Items[i] // Make a copy since we will refer to this as a reference in this loop.
		if s.APIVersion() != APIVersionV2alpha1 {
			continue
		}
		b, err := json.Marshal(&s)
		if err != nil {
			log.Warnf("error marshalling subscription in namespace %s: %s", in.GetNamespace(), err)
			continue
		}
		resp.Subscriptions = append(resp.GetSubscriptions(), b)
	}

	return resp, nil
}

// SubscriptionUpdate handles subscription update streaming for a connected client.
// Each client connection gets its own client loop that watches the informer
// and sends updates over the gRPC stream.
func (a *apiServer) SubscriptionUpdate(in *operatorv1pb.SubscriptionUpdateRequest, srv operatorv1pb.Operator_SubscriptionUpdateServer) error { //nolint:nosnakecase
	if a.closed.Load() {
		return errors.New("server is closed")
	}

	log.Info("sidecar connected for subscription updates")

	ctx := srv.Context()

	// Verify authorization via informer's WatchUpdates, which checks SPIFFE ID
	ch, cancel, err := a.subInformer.WatchUpdates(ctx, in.GetNamespace())
	if err != nil {
		return err
	}

	stream, err := sender.New(srv)
	if err != nil {
		return err
	}

	// Create a client for this connection
	client := loopsclient.New(loopsclient.Options[subapi.Subscription]{
		EventCh:     ch,
		CancelWatch: cancel,
		Stream:      stream,
		Namespace:   in.GetNamespace(),
	})
	defer client.CacheLoop()

	// Run the client - this will block until context is done or event channel closes
	if err := client.Run(ctx); err != nil {
		log.Warnf("subscription client loop ended with error: %s", err)
	}

	return nil
}
