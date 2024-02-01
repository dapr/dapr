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

package pubsub

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/modes"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

// StartSubscriptions starts the pubsub subscriptions
func (p *pubsub) StartSubscriptions(ctx context.Context) error {
	// Clean any previous state
	p.StopSubscriptions(false)

	p.lock.Lock()
	defer p.lock.Unlock()

	// If Dapr has stopped subscribing forever, return early.
	if p.stopForever {
		return nil
	}

	p.subscribing = true

	var errs []error
	for pubsubName := range p.compStore.ListPubSubs() {
		if err := p.beginPubSub(ctx, pubsubName); err != nil {
			errs = append(errs, fmt.Errorf("error occurred while beginning pubsub %s: %v", pubsubName, err))
		}
	}

	return errors.Join(errs...)
}

// StopSubscriptions to all topics and cleans the cached topics
func (p *pubsub) StopSubscriptions(forever bool) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if forever {
		// Mark if Dapr has stopped subscribing forever.
		p.stopForever = true
	}

	p.subscribing = false

	for subKey := range p.topicCancels {
		p.unsubscribeTopic(subKey)
		p.compStore.DeleteTopicRoute(subKey)
	}
}

func (p *pubsub) beginPubSub(ctx context.Context, name string) error {
	topicRoutes, err := p.topicRoutes(ctx)
	if err != nil {
		return err
	}

	v, ok := topicRoutes[name]
	if !ok {
		return nil
	}

	var errs []error
	for topic, route := range v {
		err = p.subscribeTopic(name, topic, route)
		if err != nil {
			errs = append(errs, fmt.Errorf("error occurred while beginning pubsub for topic %s on component %s: %v", topic, name, err))
		}
	}

	return errors.Join(errs...)
}

// topicRoutes returns a map of topic routes for all pubsubs.
func (p *pubsub) topicRoutes(ctx context.Context) (map[string]compstore.TopicRoutes, error) {
	if routes := p.compStore.GetTopicRoutes(); len(routes) > 0 {
		return routes, nil
	}

	topicRoutes := make(map[string]compstore.TopicRoutes)

	if p.channels.AppChannel() == nil {
		log.Warn("app channel not initialized, make sure -app-port is specified if pubsub subscription is required")
		return topicRoutes, nil
	}

	subscriptions, err := p.subscriptions(ctx)
	if err != nil {
		return nil, err
	}

	for _, s := range subscriptions {
		if topicRoutes[s.PubsubName] == nil {
			topicRoutes[s.PubsubName] = compstore.TopicRoutes{}
		}

		topicRoutes[s.PubsubName][s.Topic] = compstore.TopicRouteElem{
			Metadata:        s.Metadata,
			Rules:           s.Rules,
			DeadLetterTopic: s.DeadLetterTopic,
			BulkSubscribe:   s.BulkSubscribe,
		}
	}

	if len(topicRoutes) > 0 {
		for pubsubName, v := range topicRoutes {
			var topics string
			for topic := range v {
				if topics == "" {
					topics += topic
				} else {
					topics += " " + topic
				}
			}
			log.Infof("app is subscribed to the following topics: [%s] through pubsub=%s", topics, pubsubName)
		}
	}
	p.compStore.SetTopicRoutes(topicRoutes)
	return topicRoutes, nil
}

func (p *pubsub) subscriptions(ctx context.Context) ([]rtpubsub.Subscription, error) {
	// Check nil so that GetSubscriptions is not called twice, even if there is
	// no subscriptions.
	if subs := p.compStore.ListSubscriptions(); subs != nil {
		return subs, nil
	}

	appChannel := p.channels.AppChannel()
	if appChannel == nil {
		log.Warn("app channel not initialized, make sure -app-port is specified if pubsub subscription is required")
		return nil, nil
	}

	var (
		subscriptions []rtpubsub.Subscription
		err           error
	)

	// handle app subscriptions
	if p.isHTTP {
		subscriptions, err = rtpubsub.GetSubscriptionsHTTP(ctx, appChannel, log, p.resiliency)
	} else {
		var conn grpc.ClientConnInterface
		conn, err = p.grpc.GetAppClient()
		if err != nil {
			return nil, fmt.Errorf("error while getting app client: %w", err)
		}
		client := runtimev1pb.NewAppCallbackClient(conn)
		subscriptions, err = rtpubsub.GetSubscriptionsGRPC(ctx, client, log, p.resiliency)
	}
	if err != nil {
		return nil, err
	}

	// handle declarative subscriptions
	ds := p.declarativeSubscriptions(ctx)
	for _, s := range ds {
		skip := false

		// don't register duplicate subscriptions
		for _, sub := range subscriptions {
			if sub.PubsubName == s.PubsubName && sub.Topic == s.Topic {
				log.Warnf("two identical subscriptions found (sources: declarative, app endpoint). pubsubname: %s, topic: %s",
					s.PubsubName, s.Topic)
				skip = true
				break
			}
		}

		if !skip {
			subscriptions = append(subscriptions, s)
		}
	}

	// If subscriptions is nil, set to empty slice to prevent successive calls.
	if subscriptions == nil {
		subscriptions = make([]rtpubsub.Subscription, 0)
	}

	p.compStore.SetSubscriptions(subscriptions)
	return subscriptions, nil
}

// Refer for state store api decision
// https://github.com/dapr/dapr/blob/master/docs/decision_records/api/API-008-multi-state-store-api-design.md
func (p *pubsub) declarativeSubscriptions(ctx context.Context) []rtpubsub.Subscription {
	var subs []rtpubsub.Subscription

	switch p.mode {
	case modes.KubernetesMode:
		subs = rtpubsub.DeclarativeKubernetes(ctx, p.operatorClient, p.podName, p.namespace, log)
	case modes.StandaloneMode:
		subs = rtpubsub.DeclarativeLocal(p.resourcesPath, p.namespace, log)
	}

	// only return valid subscriptions for this app id
	i := 0
	for _, s := range subs {
		keep := false
		if len(s.Scopes) == 0 {
			keep = true
		} else {
			for _, scope := range s.Scopes {
				if scope == p.id {
					keep = true
					break
				}
			}
		}

		if keep {
			subs[i] = s
			i++
		}
	}
	return subs[:i]
}
