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
	"sync"
	"sync/atomic"
	"time"

	contribContenttype "github.com/dapr/components-contrib/contenttype"
	"github.com/dapr/components-contrib/metadata"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman"
	"github.com/dapr/kit/logger"
)

type Options struct {
	AppID           string
	Namespace       string
	PubSubName      string
	Topic           string
	PubSub          *rtpubsub.PubsubItem
	Resiliency      resiliency.Provider
	TraceSpec       *config.TracingSpec
	Route           rtpubsub.Subscription
	GRPC            *manager.Manager
	Adapter         rtpubsub.Adapter
	AdapterStreamer rtpubsub.AdapterStreamer
	ConnectionID    rtpubsub.ConnectionID
	Postman         postman.Interface
}

type Subscription struct {
	appID        string
	namespace    string
	pubsubName   string
	topic        string
	pubsub       *rtpubsub.PubsubItem
	resiliency   resiliency.Provider
	route        rtpubsub.Subscription
	tracingSpec  *config.TracingSpec
	grpc         *manager.Manager
	connectionID rtpubsub.ConnectionID

	adapterStreamer rtpubsub.AdapterStreamer
	adapter         rtpubsub.Adapter

	cancel   func(cause error)
	closed   atomic.Bool
	wg       sync.WaitGroup
	inflight atomic.Int64

	postman postman.Interface
}

var log = logger.NewLogger("dapr.runtime.processor.subscription")

const (
	BinaryCloudEventHeaderPrefix = "ce_"
	DefaultCloudEventContentType = "application/json"
	ContentTypeMetadataKey       = "content-type"
)

func New(opts Options) (*Subscription, error) {
	allowed := rtpubsub.IsOperationAllowed(opts.Topic, opts.PubSub, opts.PubSub.ScopedSubscriptions)
	if !allowed {
		return nil, fmt.Errorf("subscription to topic '%s' on pubsub '%s' is not allowed", opts.Topic, opts.PubSubName)
	}

	ctx, cancel := context.WithCancelCause(context.Background())

	s := &Subscription{
		appID:           opts.AppID,
		namespace:       opts.Namespace,
		pubsubName:      opts.PubSubName,
		topic:           opts.Topic,
		pubsub:          opts.PubSub,
		resiliency:      opts.Resiliency,
		route:           opts.Route,
		tracingSpec:     opts.TraceSpec,
		grpc:            opts.GRPC,
		cancel:          cancel,
		adapter:         opts.Adapter,
		connectionID:    opts.ConnectionID,
		adapterStreamer: opts.AdapterStreamer,
		postman:         opts.Postman,
	}

	name := s.pubsubName
	route := s.route
	policyDef := s.resiliency.ComponentInboundPolicy(name, resiliency.Pubsub)
	routeMetadata := route.Metadata

	namespaced := s.pubsub.NamespaceScoped

	if route.BulkSubscribe != nil && route.BulkSubscribe.Enabled {
		err := s.bulkSubscribeTopic(ctx, policyDef)
		if err != nil {
			cancel(nil)
			return nil, fmt.Errorf("failed to bulk subscribe to topic %s: %w", s.topic, err)
		}
		return s, nil
	}

	subscribeTopic := s.topic
	if namespaced {
		subscribeTopic = s.namespace + s.topic
	}

	err := s.pubsub.Component.Subscribe(ctx, contribpubsub.SubscribeRequest{
		Topic:    subscribeTopic,
		Metadata: routeMetadata,
	}, func(ctx context.Context, msg *contribpubsub.NewMessage) error {
		s.wg.Add(1)
		s.inflight.Add(1)
		defer func() {
			s.wg.Done()
			s.inflight.Add(-1)
		}()

		if s.closed.Load() {
			return errors.New("subscription is closed")
		}

		if msg.Metadata == nil {
			msg.Metadata = make(map[string]string, 1)
		}

		if msg.ContentType != nil {
			msg.Metadata[ContentTypeMetadataKey] = *msg.ContentType
		}

		contentType, ok := msg.Metadata[ContentTypeMetadataKey]

		if !ok {
			contentType = DefaultCloudEventContentType
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
		switch {
		case rawPayload:
			cloudEvent = contribpubsub.FromRawPayload(msg.Data, msgTopic, name)
			if traceid, ok := msg.Metadata[contribpubsub.TraceIDField]; ok {
				cloudEvent[contribpubsub.TraceIDField] = traceid
			}
			if traceparent, ok := msg.Metadata[contribpubsub.TraceParentField]; ok {
				cloudEvent[contribpubsub.TraceParentField] = traceparent
				// traceparent supersedes traceid
				cloudEvent[contribpubsub.TraceIDField] = traceparent
			}
			if tracestate, ok := msg.Metadata[contribpubsub.TraceStateField]; ok {
				cloudEvent[contribpubsub.TraceStateField] = tracestate
			}
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
		case contribContenttype.IsBinaryContentType(contentType):
			cloudEvent = make(map[string]interface{})
			// Reconstruct CloudEvent from metadata
			for k, v := range msg.Metadata {
				if strings.HasPrefix(strings.ToLower(k), BinaryCloudEventHeaderPrefix) {
					ceKey := strings.TrimPrefix(strings.ToLower(k), BinaryCloudEventHeaderPrefix)
					cloudEvent[ceKey] = v
				}
			}
			cloudEvent[contribpubsub.DataField] = msg.Data
			cloudEvent[contribpubsub.DataContentTypeField] = contentType
		default:
			// all messages consumed with "rawPayload=false" are deserialized as a CloudEvent
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

			// fallback to message metadata to propagate the tracing information
			if _, ok := cloudEvent[contribpubsub.TraceIDField]; !ok {
				if traceid, ok := msg.Metadata[contribpubsub.TraceIDField]; ok {
					cloudEvent[contribpubsub.TraceIDField] = traceid
				}
			}
			if _, ok := cloudEvent[contribpubsub.TraceParentField]; !ok {
				if traceparent, ok := msg.Metadata[contribpubsub.TraceParentField]; ok {
					cloudEvent[contribpubsub.TraceParentField] = traceparent
					// traceparent supersedes traceid
					cloudEvent[contribpubsub.TraceIDField] = traceparent
				}
			}
			if _, ok := cloudEvent[contribpubsub.TraceStateField]; !ok {
				if tracestate, ok := msg.Metadata[contribpubsub.TraceStateField]; ok {
					cloudEvent[contribpubsub.TraceStateField] = tracestate
				}
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
			CloudEvent:   cloudEvent,
			Data:         data,
			Topic:        msgTopic,
			Metadata:     msg.Metadata,
			Path:         routePath,
			PubSub:       name,
			SubscriberID: s.connectionID,
		}
		policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
		_, err = policyRunner(func(ctx context.Context) (any, error) {
			pErr := s.postman.Deliver(ctx, sm)

			var rErr *rterrors.RetriableError
			if errors.As(pErr, &rErr) {
				log.Warnf("encountered a retriable error while publishing a subscribed message to topic %s, err: %v", msgTopic, rErr.Unwrap())
			} else if errors.Is(pErr, rtpubsub.ErrMessageDropped) {
				// send dropped message to dead letter queue if configured
				if route.DeadLetterTopic != "" {
					derr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
					if derr != nil {
						log.Warnf("failed to send dropped message to dead letter queue for topic %s: %v", msgTopic, derr)
						return nil, pErr
					}
				}
				return nil, nil
			} else if pErr != nil {
				log.Errorf("encountered a non-retriable error while publishing a subscribed message to topic %s, err: %v", msgTopic, pErr)
			}
			return nil, pErr
		})
		// when runtime shutting down, don't send to DLQ
		if err != nil && err != context.Canceled {
			// Sending msg to dead letter queue.
			// If no DLQ is configured, return error for backwards compatibility (component-level retry).
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
		return err
	})
	if err != nil {
		cancel(nil)
		return nil, fmt.Errorf("failed to subscribe to topic %s: %w", s.topic, err)
	}

	return s, nil
}

func (s *Subscription) Stop(err ...error) {
	s.closed.Store(true)
	inflight := s.inflight.Load() > 0

	s.wg.Wait()
	if s.adapterStreamer != nil {
		s.adapterStreamer.Close(s.adapterStreamer.StreamerKey(s.pubsubName, s.topic), s.connectionID)
	}
	// If there were in-flight requests then wait some time for the result to be
	// sent to the broker. This is because the message result context is
	// disparate.
	if inflight {
		time.Sleep(time.Millisecond * 400)
	}
	if err != nil && len(err) > 0 {
		s.cancel(errors.Join(err...))
		return
	}

	s.cancel(nil)
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
