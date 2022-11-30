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

package server

import (
	"context"
	"errors"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/golang/protobuf/ptypes/empty"
)

func getKey(pubsub, topic string) string {
	return pubsub + "-" + topic
}

func (s *Server) AddTopicSubscription(sub *runtimev1pb.TopicSubscription) {
	s.subscriptions[getKey(sub.PubsubName, sub.Topic)] = sub
}

// ListTopicSubscriptions is invoked by Dapr to get the list of topics the app wants to subscribe to.
func (s *Server) ListTopicSubscriptions(ctx context.Context, in *empty.Empty) (*runtimev1pb.ListTopicSubscriptionsResponse, error) {
	var subs []*runtimev1pb.TopicSubscription
	for _, sub := range s.subscriptions {
		subs = append(subs, sub)
	}

	return &runtimev1pb.ListTopicSubscriptionsResponse{Subscriptions: subs}, nil
}

// OnTopicEvent is invoked by Dapr when a topic event is fired.
func (s *Server) OnTopicEvent(ctx context.Context, in *runtimev1pb.TopicEventRequest) (*runtimev1pb.TopicEventResponse, error) {
	if in == nil || in.PubsubName == "" || in.Topic == "" {
		return &runtimev1pb.TopicEventResponse{Status: runtimev1pb.TopicEventResponse_DROP}, errors.New("pubsubName and topic are required")
	}

	_, ok := s.subscriptions[getKey(in.PubsubName, in.Topic)]
	if !ok {
		return &runtimev1pb.TopicEventResponse{Status: runtimev1pb.TopicEventResponse_DROP}, errors.New("topic subscription not found")
	}

	s.onTopicEventNotifyChan <- struct{}{}
	return &runtimev1pb.TopicEventResponse{Status: runtimev1pb.TopicEventResponse_SUCCESS}, nil
}

// OnTopicEvent is invoked by Dapr when a bulk topic event is fired.
func (s *Server) OnBulkTopicEventAlpha1(ctx context.Context, in *runtimev1pb.TopicEventBulkRequest) (*runtimev1pb.TopicEventBulkResponse, error) {
	var statuses []*runtimev1pb.TopicEventBulkResponseEntry
	if in == nil || in.PubsubName == "" || in.Topic == "" {
		return &runtimev1pb.TopicEventBulkResponse{Statuses: statuses}, errors.New("pubsubName and topic are required")
	}

	_, ok := s.subscriptions[getKey(in.PubsubName, in.Topic)]
	if !ok {
		return &runtimev1pb.TopicEventBulkResponse{Statuses: statuses}, errors.New("topic subscription not found")
	}

	for _, entry := range in.Entries {
		statuses = append(statuses, &runtimev1pb.TopicEventBulkResponseEntry{
			EntryId: entry.EntryId,
			Status:  runtimev1pb.TopicEventResponse_SUCCESS})
	}

	s.onBulkTopicEventNotifyChan <- len(in.Entries)
	return &runtimev1pb.TopicEventBulkResponse{Statuses: statuses}, nil
}
