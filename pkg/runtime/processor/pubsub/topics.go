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
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/metadata"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

const (
	metadataKeyPubSub = "pubsubName"
)

func (p *pubsub) subscribeTopic(name, topic string, route compstore.TopicRouteElem) error {
	subKey := topicKey(name, topic)

	pubSub, ok := p.compStore.GetPubSub(name)
	if !ok {
		return fmt.Errorf("pubsub '%s' not found", name)
	}

	allowed := p.isOperationAllowed(name, topic, pubSub.ScopedSubscriptions)
	if !allowed {
		return fmt.Errorf("subscription to topic '%s' on pubsub '%s' is not allowed", topic, name)
	}

	log.Debugf("subscribing to topic='%s' on pubsub='%s'", topic, name)

	if _, ok := p.topicCancels[subKey]; ok {
		return fmt.Errorf("cannot subscribe to topic '%s' on pubsub '%s': the subscription already exists", topic, name)
	}

	ctx, cancel := context.WithCancel(context.Background())
	policyDef := p.resiliency.ComponentInboundPolicy(name, resiliency.Pubsub)
	routeMetadata := route.Metadata

	namespaced := pubSub.NamespaceScoped

	if route.BulkSubscribe != nil && route.BulkSubscribe.Enabled {
		err := p.bulkSubscribeTopic(ctx, policyDef, name, topic, route, namespaced)
		if err != nil {
			cancel()
			return fmt.Errorf("failed to bulk subscribe to topic %s: %w", topic, err)
		}
		p.topicCancels[subKey] = cancel
		return nil
	}

	subscribeTopic := topic
	if namespaced {
		subscribeTopic = p.namespace + topic
	}

	err := pubSub.Component.Subscribe(ctx, contribpubsub.SubscribeRequest{
		Topic:    subscribeTopic,
		Metadata: routeMetadata,
	}, func(ctx context.Context, msg *contribpubsub.NewMessage) error {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]string, 1)
		}

		msg.Metadata[metadataKeyPubSub] = name

		msgTopic := msg.Topic
		if pubSub.NamespaceScoped {
			msgTopic = strings.Replace(msgTopic, p.namespace, "", 1)
		}

		rawPayload, err := metadata.IsRawPayload(route.Metadata)
		if err != nil {
			log.Errorf("error deserializing pubsub metadata: %s", err)
			if route.DeadLetterTopic != "" {
				if dlqErr := p.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic); dlqErr == nil {
					// dlq has been configured and message is successfully sent to dlq.
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), msgTopic, 0)
					return nil
				}
			}
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), msgTopic, 0)
			return err
		}

		var cloudEvent map[string]interface{}
		data := msg.Data
		if rawPayload {
			cloudEvent = contribpubsub.FromRawPayload(msg.Data, msgTopic, name)
			data, err = json.Marshal(cloudEvent)
			if err != nil {
				log.Errorf("error serializing cloud event in pubsub %s and topic %s: %s", name, msgTopic, err)
				if route.DeadLetterTopic != "" {
					if dlqErr := p.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic); dlqErr == nil {
						// dlq has been configured and message is successfully sent to dlq.
						diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), msgTopic, 0)
						return nil
					}
				}
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), msgTopic, 0)
				return err
			}
		} else {
			err = json.Unmarshal(msg.Data, &cloudEvent)
			if err != nil {
				log.Errorf("error deserializing cloud event in pubsub %s and topic %s: %s", name, msgTopic, err)
				if route.DeadLetterTopic != "" {
					if dlqErr := p.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic); dlqErr == nil {
						// dlq has been configured and message is successfully sent to dlq.
						diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), msgTopic, 0)
						return nil
					}
				}
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), msgTopic, 0)
				return err
			}
		}

		if contribpubsub.HasExpired(cloudEvent) {
			log.Warnf("dropping expired pub/sub event %v as of %v", cloudEvent[contribpubsub.IDField], cloudEvent[contribpubsub.ExpirationField])
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), msgTopic, 0)

			if route.DeadLetterTopic != "" {
				_ = p.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
			}
			return nil
		}

		routePath, shouldProcess, err := findMatchingRoute(route.Rules, cloudEvent)
		if err != nil {
			log.Errorf("error finding matching route for event %v in pubsub %s and topic %s: %s", cloudEvent[contribpubsub.IDField], name, msgTopic, err)
			if route.DeadLetterTopic != "" {
				if dlqErr := p.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic); dlqErr == nil {
					// dlq has been configured and message is successfully sent to dlq.
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), msgTopic, 0)
					return nil
				}
			}
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), msgTopic, 0)
			return err
		}
		if !shouldProcess {
			// The event does not match any route specified so ignore it.
			log.Debugf("no matching route for event %v in pubsub %s and topic %s; skipping", cloudEvent[contribpubsub.IDField], name, msgTopic)
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), msgTopic, 0)
			if route.DeadLetterTopic != "" {
				_ = p.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
			}
			return nil
		}

		sm := &subscribedMessage{
			cloudEvent: cloudEvent,
			data:       data,
			topic:      msgTopic,
			metadata:   msg.Metadata,
			path:       routePath,
			pubsub:     name,
		}
		policyRunner := resiliency.NewRunner[any](ctx, policyDef)
		_, err = policyRunner(func(ctx context.Context) (any, error) {
			var pErr error
			if p.isHTTP {
				pErr = p.publishMessageHTTP(ctx, sm)
			} else {
				pErr = p.publishMessageGRPC(ctx, sm)
			}
			var rErr *rterrors.RetriableError
			if errors.As(pErr, &rErr) {
				log.Warnf("encountered a retriable error while publishing a subscribed message to topic %s, err: %v", msgTopic, rErr.Unwrap())
			} else if errors.Is(pErr, runtimePubsub.ErrMessageDropped) {
				// send dropped message to dead letter queue if configured
				if route.DeadLetterTopic != "" {
					derr := p.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
					if derr != nil {
						log.Warnf("failed to send dropped message to dead letter queue for topic %s: %v", msgTopic, derr)
					}
				}
				return nil, nil
			} else if pErr != nil {
				log.Errorf("encountered a non-retriable error while publishing a subscribed message to topic %s, err: %v", msgTopic, pErr)
			}
			return nil, pErr
		})
		if err != nil && err != context.Canceled {
			// Sending msg to dead letter queue.
			// If no DLQ is configured, return error for backwards compatibility (component-level retry).
			if route.DeadLetterTopic == "" {
				return err
			}
			_ = p.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
			return nil
		}
		return err
	})
	if err != nil {
		cancel()
		return fmt.Errorf("failed to subscribe to topic %s: %w", topic, err)
	}
	p.topicCancels[subKey] = cancel
	return nil
}

func (p *pubsub) unsubscribeTopic(subKey string) {
	// Don't lock as caller is expected to do so.
	cancel, ok := p.topicCancels[subKey]
	if !ok {
		log.Warnf("cannot unsubscribe from pubsub topic '%s': the subscription does not exist", subKey)
		return
	}

	if cancel != nil {
		cancel()
	}

	delete(p.topicCancels, subKey)
}

func (p *pubsub) isOperationAllowed(name string, topic string, scopedTopics []string) bool {
	var inAllowedTopics, inProtectedTopics bool

	pubSub, ok := p.compStore.GetPubSub(name)
	if !ok {
		return false
	}

	// first check if allowedTopics contain it
	if len(pubSub.AllowedTopics) > 0 {
		for _, t := range pubSub.AllowedTopics {
			if t == topic {
				inAllowedTopics = true
				break
			}
		}
		if !inAllowedTopics {
			return false
		}
	}

	// check if topic is protected
	if len(pubSub.ProtectedTopics) > 0 {
		for _, t := range pubSub.ProtectedTopics {
			if t == topic {
				inProtectedTopics = true
				break
			}
		}
	}

	// if topic is protected then a scope must be applied
	if !inProtectedTopics && len(scopedTopics) == 0 {
		return true
	}

	// check if a granular scope has been applied
	allowedScope := false
	for _, t := range scopedTopics {
		if t == topic {
			allowedScope = true
			break
		}
	}

	return allowedScope
}

// topicKey returns "componentName||topicName", which is used as key for some
// maps.
func topicKey(componentName, topicName string) string {
	return componentName + "||" + topicName
}

func (p *pubsub) sendToDeadLetter(ctx context.Context, name string, msg *contribpubsub.NewMessage, deadLetterTopic string) error {
	req := &contribpubsub.PublishRequest{
		Data:        msg.Data,
		PubsubName:  name,
		Topic:       deadLetterTopic,
		Metadata:    msg.Metadata,
		ContentType: msg.ContentType,
	}

	if err := p.Publish(ctx, req); err != nil {
		log.Errorf("error sending message to dead letter, origin topic: %s dead letter topic %s err: %w", msg.Topic, deadLetterTopic, err)
		return err
	}

	return nil
}
