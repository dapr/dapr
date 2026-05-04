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

	cancel func(cause error)
	// drainMu makes the handler's "check drainSealed → wg.Add" region
	// atomic with respect to Stop's "set drainSealed → wg.Wait" region.
	// Handlers take RLock; Stop takes Lock when sealing. Without it, a
	// handler that reads drainSealed=false can still race past Stop's
	// Wait and panic with "WaitGroup is reused before previous Wait has
	// returned" — exactly the failure mode contrib retry loops surface.
	drainMu sync.RWMutex
	// drainSealed is set after the drain phase completes (inflight=0) and
	// just before the final wg.Wait. It bars any new wg.Add calls so that
	// the subsequent Wait can never race with an incoming Add. Handlers
	// check this BEFORE calling wg.Add (under drainMu.RLock) and take the
	// <-ctx.Done() block path if set.
	drainSealed atomic.Bool
	closed      atomic.Bool
	wg          sync.WaitGroup
	inflight    atomic.Int64

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
		// Hold drainMu.RLock across the drainSealed check and wg.Add so
		// the two are atomic with respect to Stop, which takes the write
		// lock before sealing and waiting. Without this, a handler that
		// reads drainSealed=false can be preempted past Stop's wg.Wait
		// and panic on the subsequent wg.Add.
		s.drainMu.RLock()
		if s.drainSealed.Load() {
			s.drainMu.RUnlock()
			<-ctx.Done()
			return ctx.Err()
		}
		s.wg.Add(1)
		s.inflight.Add(1)
		s.drainMu.RUnlock()

		if s.closed.Load() {
			// Release the WaitGroup before blocking to avoid deadlocking
			// with Subscription.Stop() which calls wg.Wait().
			s.wg.Done()
			s.inflight.Add(-1)
			// Block until the handler context is cancelled rather than
			// returning an error. Returning an error causes the broker to
			// NACK the message (e.g. routing it to a dead-letter queue),
			// which is incorrect during graceful shutdown. By blocking, the
			// message is held until the broker connection is closed, allowing
			// the broker to redeliver the message to another consumer. We
			// block on the handler context (tied to the broker's pull stream)
			// rather than the subscription context because the subscription
			// context may already be cancelled by the time this message
			// arrives.
			<-ctx.Done()
			return ctx.Err()
		}

		wgReleased := false
		defer func() {
			if !wgReleased {
				s.wg.Done()
				s.inflight.Add(-1)
			}
		}()

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
				dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
				if dlqErr == nil {
					// dlq has been configured and message is successfully sent to dlq.
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), "", msgTopic, 0)
					return nil
				}
			}

			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), "", msgTopic, 0)

			return err
		}

		var cloudEvent map[string]any

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
					dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
					if dlqErr == nil {
						// dlq has been configured and message is successfully sent to dlq.
						diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), "", msgTopic, 0)
						return nil
					}
				}

				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), "", msgTopic, 0)

				return err
			}
		case contribContenttype.IsBinaryContentType(contentType):
			cloudEvent = make(map[string]any)
			// Reconstruct CloudEvent from metadata
			for k, v := range msg.Metadata {
				if after, ok0 := strings.CutPrefix(strings.ToLower(k), BinaryCloudEventHeaderPrefix); ok0 {
					ceKey := after
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
					dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
					if dlqErr == nil {
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
				dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
				if dlqErr == nil {
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
		// When the subscription is closing (e.g. during shutdown or
		// hot-reload), block on the handler context rather than returning an
		// error. Returning an error causes the broker to NACK the message
		// (e.g. routing it to a dead-letter queue), which is incorrect
		// during graceful shutdown. Release the WaitGroup before blocking to
		// avoid deadlocking with Subscription.Stop() which calls wg.Wait().
		if err != nil && (s.closed.Load() || errors.Is(err, rtpubsub.ErrSubscriptionClosed)) {
			wgReleased = true
			s.wg.Done()
			s.inflight.Add(-1)
			<-ctx.Done()
			return ctx.Err()
		}

		// when runtime shutting down, don't send to DLQ
		if err != nil && err != context.Canceled {
			// Sending msg to dead letter queue.
			// If no DLQ is configured, return error for backwards compatibility (component-level retry).
			if route.DeadLetterTopic != "" {
				dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
				if dlqErr == nil {
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
	cause := errors.Join(err...)
	graceful := errors.Is(cause, contribpubsub.ErrGracefulShutdown)

	// On graceful shutdown, if the underlying pubsub component supports it,
	// pause fetching new messages from the broker. While paused, the
	// session and partition assignments stay alive — and importantly, the
	// heartbeat loop continues — so any messages already buffered locally
	// can still be delivered to the app via postman.Deliver. The actual
	// close (e.g. Kafka's consumerGroup.Close → LeaveGroup) happens via
	// the existing s.cancel path at the end.
	paused := false
	if graceful {
		if pausable, ok := s.pubsub.Component.(contribpubsub.PausableSubscriber); ok {
			log.Infof("pausing subscription on topic %s during graceful shutdown", s.topic)
			if perr := pausable.Pause(context.Background()); perr != nil {
				log.Warnf("failed to pause subscription on topic %s during graceful shutdown: %v", s.topic, perr)
			} else {
				paused = true
			}
		}
	}

	// When pausing succeeded, leave s.closed=false during the drain so
	// handlers continue delivering buffered messages to the app and we get
	// SUCCESS/DROP/RETRY back from the app. Pending offsets get marked and
	// will be flushed by Sarama's auto-commit and the final commit during
	// session.release(). For non-pausable components or non-graceful Stop,
	// fall back to close-first behavior — without Pause we have no way to
	// bound the buffer growth, so blocking incoming work is the only safe
	// option.
	if !paused {
		s.closed.Store(true)
	}

	// Snapshot inflight up front, then promote it to true if any work is
	// observed during the drain loop. In the paused (drain-to-app) path
	// closed stays false during drain, so handler invocations can start
	// after the initial snapshot — without this update, the 400ms
	// ack-settle below would be skipped even though the broker is still
	// awaiting our acks.
	inflight := s.inflight.Load() > 0

	// Drain via atomic polling rather than wg.Wait. wg.Wait alone is
	// unsafe here because contrib retry loops can call the handler
	// repeatedly with wg.Add(1)/wg.Done() bracketed around backoff
	// sleeps. Between retries the counter momentarily hits 0; if Wait
	// was in progress it can start to unwind and race the next retry's
	// Add(1), tripping the "WaitGroup is reused before previous Wait
	// has returned" panic. atomic.Int64 polling has no such restriction.
	//
	// No inner timeout here — the runner's graceful-shutdown-duration
	// caps the whole close phase, so a stuck handler can't hang shutdown
	// indefinitely.
	const drainPollInterval = 20 * time.Millisecond
	for s.inflight.Load() > 0 {
		inflight = true
		time.Sleep(drainPollInterval)
	}

	// Take the write lock to seal the drain atomically with respect to
	// any handler that may be sitting between its drainSealed check and
	// its wg.Add. Once we release, all future handlers will observe
	// drainSealed=true under their RLock and skip the Add entirely, so
	// wg.Wait below cannot race with a concurrent Add.
	s.drainMu.Lock()
	s.drainSealed.Store(true)
	s.closed.Store(true)
	s.drainMu.Unlock()
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

	if len(err) > 0 {
		s.cancel(cause)
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

	err := s.adapter.Publish(ctx, req)
	if err != nil {
		log.Errorf("error sending message to dead letter, origin topic: %s dead letter topic %s err: %v", msg.Topic, deadLetterTopic, err)
		return err
	}

	return nil
}

// findMatchingRoute selects the path based on routing rules. If there are
// no matching rules, the route-level path is used.
func findMatchingRoute(rules []*rtpubsub.Rule, cloudEvent any) (path string, shouldProcess bool, err error) {
	hasRules := len(rules) > 0
	if hasRules {
		data := map[string]any{
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

func matchRoutingRule(rules []*rtpubsub.Rule, data map[string]any) (*rtpubsub.Rule, error) {
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
