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

package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/metadata"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/kit/logger"
)

type Options struct {
	AppID           string
	Namespace       string
	PubSubName      string
	Topic           string
	IsHTTP          bool
	PubSub          *rtpubsub.PubsubItem
	Resiliency      resiliency.Provider
	TraceSpec       *config.TracingSpec
	Route           rtpubsub.Subscription
	Channels        *channels.Channels
	GRPC            *manager.Manager
	Adapter         rtpubsub.Adapter
	AdapterStreamer rtpubsub.AdapterStreamer
}

type Subscription struct {
	appID           string
	namespace       string
	pubsubName      string
	topic           string
	isHTTP          bool
	pubsub          *rtpubsub.PubsubItem
	resiliency      resiliency.Provider
	route           rtpubsub.Subscription
	tracingSpec     *config.TracingSpec
	channels        *channels.Channels
	grpc            *manager.Manager
	adapter         rtpubsub.Adapter
	adapterStreamer rtpubsub.AdapterStreamer

	cancel func()
}

var log = logger.NewLogger("dapr.runtime.processor.pubsub.subscription")

func New(opts Options) (*Subscription, error) {
	allowed := rtpubsub.IsOperationAllowed(opts.Topic, opts.PubSub, opts.PubSub.ScopedSubscriptions)
	if !allowed {
		return nil, fmt.Errorf("subscription to topic '%s' on pubsub '%s' is not allowed", opts.Topic, opts.PubSubName)
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &Subscription{
		appID:           opts.AppID,
		namespace:       opts.Namespace,
		pubsubName:      opts.PubSubName,
		topic:           opts.Topic,
		isHTTP:          opts.IsHTTP,
		pubsub:          opts.PubSub,
		resiliency:      opts.Resiliency,
		route:           opts.Route,
		tracingSpec:     opts.TraceSpec,
		channels:        opts.Channels,
		grpc:            opts.GRPC,
		cancel:          cancel,
		adapter:         opts.Adapter,
		adapterStreamer: opts.AdapterStreamer,
	}

	name := s.pubsubName
	route := s.route
	policyDef := s.resiliency.ComponentInboundPolicy(name, resiliency.Pubsub)
	routeMetadata := route.Metadata

	namespaced := s.pubsub.NamespaceScoped

	if route.BulkSubscribe != nil && route.BulkSubscribe.Enabled {
		err := s.bulkSubscribeTopic(ctx, policyDef)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to bulk subscribe to topic %s: %w", s.topic, err)
		}
		return s, nil
	}

	// TODO: @joshvanl: move subsscribedTopic to struct
	subscribeTopic := s.topic
	if namespaced {
		subscribeTopic = s.namespace + s.topic
	}

	err := s.pubsub.Component.Subscribe(ctx, contribpubsub.SubscribeRequest{
		Topic:    subscribeTopic,
		Metadata: routeMetadata,
	}, func(ctx context.Context, msg *contribpubsub.NewMessage) error {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]string, 1)
		}

		msg.Metadata[rtpubsub.MetadataKeyPubSub] = name

		msgTopic := msg.Topic
		if s.pubsub.NamespaceScoped {
			msgTopic = strings.Replace(msgTopic, s.namespace, "", 1)
		}

		rawPayload, err := metadata.IsRawPayload(route.Metadata)
		if err != nil {
			log.Errorf("error deserializing pubsub metadata: %s", err)
			if route.DeadLetterTopic != "" {
				if dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic); dlqErr == nil {
					// dlq has been configured and message is successfully sent to dlq.
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), "", msgTopic, 0)
					return nil
				}
			}
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), "", msgTopic, 0)
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
					if dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic); dlqErr == nil {
						// dlq has been configured and message is successfully sent to dlq.
						diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), "", msgTopic, 0)
						return nil
					}
				}
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), "", msgTopic, 0)
				return err
			}
		} else {
			err = json.Unmarshal(msg.Data, &cloudEvent)
			if err != nil {
				log.Errorf("error deserializing cloud event in pubsub %s and topic %s: %s", name, msgTopic, err)
				if route.DeadLetterTopic != "" {
					if dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic); dlqErr == nil {
						// dlq has been configured and message is successfully sent to dlq.
						diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), "", msgTopic, 0)
						return nil
					}
				}
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), "", msgTopic, 0)
				return err
			}
		}

		if contribpubsub.HasExpired(cloudEvent) {
			log.Warnf("dropping expired pub/sub event %v as of %v", cloudEvent[contribpubsub.IDField], cloudEvent[contribpubsub.ExpirationField])
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), "", msgTopic, 0)

			if route.DeadLetterTopic != "" {
				_ = s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
			}
			return nil
		}

		routePath, shouldProcess, err := findMatchingRoute(route.Rules, cloudEvent)
		if err != nil {
			log.Errorf("error finding matching route for event %v in pubsub %s and topic %s: %s", cloudEvent[contribpubsub.IDField], name, msgTopic, err)
			if route.DeadLetterTopic != "" {
				if dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic); dlqErr == nil {
					// dlq has been configured and message is successfully sent to dlq.
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), "", msgTopic, 0)
					return nil
				}
			}
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), "", msgTopic, 0)
			return err
		}

		if !shouldProcess {
			// The event does not match any route specified so ignore it.
			log.Debugf("no matching route for event %v in pubsub %s and topic %s; skipping", cloudEvent[contribpubsub.IDField], name, msgTopic)
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), strings.ToLower(string(contribpubsub.Success)), msgTopic, 0)
			if route.DeadLetterTopic != "" {
				_ = s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
			}
			return nil
		}

		sm := &rtpubsub.SubscribedMessage{
			CloudEvent: cloudEvent,
			Data:       data,
			Topic:      msgTopic,
			Metadata:   msg.Metadata,
			Path:       routePath,
			PubSub:     name,
		}
		policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
		_, err = policyRunner(func(ctx context.Context) (any, error) {
			var pErr error
			if s.adapterStreamer != nil {
				pErr = s.adapterStreamer.Publish(ctx, sm)
			} else {
				if s.isHTTP {
					pErr = s.publishMessageHTTP(ctx, sm)
				} else {
					pErr = s.publishMessageGRPC(ctx, sm)
				}
			}

			var rErr *rterrors.RetriableError
			if errors.As(pErr, &rErr) {
				log.Warnf("encountered a retriable error while publishing a subscribed message to topic %s, err: %v", msgTopic, rErr.Unwrap())
			} else if errors.Is(pErr, rtpubsub.ErrMessageDropped) {
				// send dropped message to dead letter queue if configured
				if route.DeadLetterTopic != "" {
					derr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
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
			_ = s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
			return nil
		}
		return err
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to subscribe to topic %s: %w", s.topic, err)
	}

	return s, nil
}

func (s *Subscription) Stop() {
	s.cancel()
}

func (s *Subscription) sendToDeadLetter(ctx context.Context, name string, msg *contribpubsub.NewMessage, deadLetterTopic string) error {
	req := &contribpubsub.PublishRequest{
		Data:        msg.Data,
		PubsubName:  name,
		Topic:       deadLetterTopic,
		Metadata:    msg.Metadata,
		ContentType: msg.ContentType,
	}

	if err := s.adapter.Publish(ctx, req); err != nil {
		log.Errorf("error sending message to dead letter, origin topic: %s dead letter topic %s err: %w", msg.Topic, deadLetterTopic, err)
		return err
	}

	return nil
}

// findMatchingRoute selects the path based on routing rules. If there are
// no matching rules, the route-level path is used.
func findMatchingRoute(rules []*rtpubsub.Rule, cloudEvent interface{}) (path string, shouldProcess bool, err error) {
	hasRules := len(rules) > 0
	if hasRules {
		data := map[string]interface{}{
			"event": cloudEvent,
		}
		rule, err := matchRoutingRule(rules, data)
		if err != nil {
			return "", false, err
		}
		if rule != nil {
			return rule.Path, true, nil
		}
	}

	return "", false, nil
}

func matchRoutingRule(rules []*rtpubsub.Rule, data map[string]interface{}) (*rtpubsub.Rule, error) {
	for _, rule := range rules {
		if rule.Match == nil || len(rule.Match.String()) == 0 {
			return rule, nil
		}
		iResult, err := rule.Match.Eval(data)
		if err != nil {
			return nil, err
		}
		result, ok := iResult.(bool)
		if !ok {
			return nil, fmt.Errorf("the result of match expression %s was not a boolean", rule.Match)
		}

		if result {
			return rule, nil
		}
	}

	return nil, nil
}
