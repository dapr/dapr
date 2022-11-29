package main

import (
	"context"
	"errors"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/golang/protobuf/ptypes/empty"
)

func getKey(pubsub, topic string) string {
	return pubsub + "-" + topic
}

func (s *Server) addTopicSubscription(sub *runtimev1pb.TopicSubscription) {
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
	return &runtimev1pb.TopicEventBulkResponse{Statuses: statuses}, nil
}
