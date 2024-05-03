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
	"fmt"
	"sync"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/emptypb"
	"sigs.k8s.io/controller-runtime/pkg/client"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/operator/api/authz"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

type SubscriptionUpdateEvent struct {
	Subscription *subapi.Subscription
	EventType    operatorv1pb.ResourceEventType
}

func (a *apiServer) OnSubscriptionUpdated(ctx context.Context, eventType operatorv1pb.ResourceEventType, subscription *subapi.Subscription) {
	a.connLock.Lock()
	var wg sync.WaitGroup
	wg.Add(len(a.allSubscriptionUpdateChan))
	for _, connUpdateChan := range a.allSubscriptionUpdateChan {
		go func(connUpdateChan chan *SubscriptionUpdateEvent) {
			defer wg.Done()
			select {
			case connUpdateChan <- &SubscriptionUpdateEvent{
				Subscription: subscription,
				EventType:    eventType,
			}:
			case <-ctx.Done():
			}
		}(connUpdateChan)
	}
	wg.Wait()
	a.connLock.Unlock()
}

// ListSubscriptions returns a list of Dapr pub/sub subscriptions.
func (a *apiServer) ListSubscriptions(ctx context.Context, in *emptypb.Empty) (*operatorv1pb.ListSubscriptionsResponse, error) {
	return a.ListSubscriptionsV2(ctx, &operatorv1pb.ListSubscriptionsRequest{})
}

// ListSubscriptionsV2 returns a list of Dapr pub/sub subscriptions. Use ListSubscriptionsRequest to expose pod info.
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
			log.Warnf("error marshalling subscription for pod %s/%s: %s", in.GetNamespace(), in.GetPodName(), err)
			continue
		}
		resp.Subscriptions = append(resp.GetSubscriptions(), b)
	}

	return resp, nil
}

func (a *apiServer) SubscriptionUpdate(in *operatorv1pb.SubscriptionUpdateRequest, srv operatorv1pb.Operator_SubscriptionUpdateServer) error { //nolint:nosnakecase
	if _, err := authz.Request(srv.Context(), in.GetNamespace()); err != nil {
		return err
	}

	log.Info("sidecar connected for subscription updates")
	keyObj, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	key := keyObj.String()

	a.connLock.Lock()
	updateChan := make(chan *SubscriptionUpdateEvent)
	a.allSubscriptionUpdateChan[key] = updateChan
	a.connLock.Unlock()

	defer func() {
		a.connLock.Lock()
		defer a.connLock.Unlock()
		delete(a.allSubscriptionUpdateChan, key)
	}()

	updateSubscriptionFunc := func(ctx context.Context, t operatorv1pb.ResourceEventType, sub *subapi.Subscription) {
		if sub.Namespace != in.GetNamespace() {
			return
		}

		b, err := json.Marshal(&sub)
		if err != nil {
			log.Warnf("error serializing subscription %s for pod %s/%s: %s", sub.GetName(), in.GetNamespace(), in.GetPodName(), err)
			return
		}

		err = srv.Send(&operatorv1pb.SubscriptionUpdateEvent{
			Subscription: b,
			Type:         t,
		})
		if err != nil {
			log.Warnf("error updating sidecar with subscroption %s to pod %s/%s: %s", sub.GetName(), in.GetNamespace(), in.GetPodName(), err)
			return
		}

		log.Debugf("updated sidecar with subscription %s %s to pod %s/%s", t.String(), sub.GetName(), in.GetNamespace(), in.GetPodName())
	}

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-srv.Context().Done():
			return nil
		case c, ok := <-updateChan:
			if !ok {
				return nil
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				updateSubscriptionFunc(srv.Context(), c.EventType, c.Subscription)
			}()
		}
	}
}
