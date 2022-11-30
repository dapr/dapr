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

package main

import (
	"log"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/perf/pubsub_bulk_subscribe_grpc/pkg/server"
)

const (
	pubSubName = "inmemorypubsub"
	topic      = "perf-sub-topic"
)

func main() {
	s, err := server.NewService(":3000")
	if err != nil {
		log.Fatalf("failed to create service: %v", err)
	}

	sub := &runtimev1pb.TopicSubscription{
		PubsubName: pubSubName,
		Topic:      topic,
	}
	s.AddTopicSubscription(sub)

	bulkSub := &runtimev1pb.TopicSubscription{
		PubsubName: pubSubName,
		Topic:      "bulk-" + topic,
		Metadata:   map[string]string{"bulkSubscribe": "true"},
	}
	s.AddTopicSubscription(bulkSub)

	defer s.Stop()
	if err = s.Start(); err != nil {
		log.Fatalf("failed to start service: %v", err)
	}
}
