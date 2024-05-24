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

package pubsub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

type streamer struct {
	pubsub      *pubsub
	subscribers map[string]*streamconn

	lock sync.RWMutex
}

func (s *streamer) Subscribe(stream rtv1pb.Dapr_SubscribeTopicEventsAlpha1Server) error {
	ireq, err := stream.Recv()
	if err != nil {
		return err
	}

	req := ireq.GetInitialRequest()

	if req == nil {
		return errors.New("initial request is required")
	}

	if len(req.GetPubsubName()) == 0 {
		return errors.New("pubsubName name is required")
	}

	if len(req.GetTopic()) == 0 {
		return errors.New("topic is required")
	}

	s.lock.Lock()
	key := streamerKey(req.GetPubsubName(), req.GetTopic())
	if _, ok := s.subscribers[key]; ok {
		s.lock.Unlock()
		return fmt.Errorf("already subscribed to pubsub %q topic %q", req.GetPubsubName(), req.GetTopic())
	}

	conn := &streamconn{
		stream:           stream,
		publishResponses: make(map[string]chan *rtv1pb.SubscribeTopicEventsResponseAlpha1),
		s:                s,
	}
	s.subscribers[key] = conn

	decSub := subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: key},
		Spec: subapi.SubscriptionSpec{
			Pubsubname:      req.GetPubsubName(),
			Topic:           req.GetTopic(),
			Metadata:        req.GetMetadata(),
			DeadLetterTopic: req.GetDeadLetterTopic(),
			Routes:          subapi.Routes{Default: "/"},
		},
	}

	// TODO: @joshvanl: handle duplicates.
	if err := s.pubsub.compStore.AddDeclarativeSubscription(decSub); err != nil {
		delete(s.subscribers, key)
		s.lock.Unlock()
		return err
	}

	if err := s.pubsub.ReloadSubscriptions(context.TODO()); err != nil {
		s.pubsub.compStore.DeleteDeclaraiveSubscription(key)
		delete(s.subscribers, key)
		s.lock.Unlock()
		return err
	}

	log.Infof("Subscribing to pubsub '%s' topic '%s'", req.GetPubsubName(), req.GetTopic())
	s.lock.Unlock()

	defer func() {
		s.lock.Lock()
		s.pubsub.compStore.DeleteDeclaraiveSubscription(key)
		delete(s.subscribers, key)
		s.lock.Unlock()
		// TODO: @joshvanl: use context derived from main context.
		if err := s.pubsub.ReloadSubscriptions(context.TODO()); err != nil {
			log.Errorf("Error reloading subscriptions after Subscribe shutdown: %s", err)
		}
	}()

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		resp, err := stream.Recv()

		if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
			return err
		}

		if err != nil {
			log.Errorf("error receiving message from client stream: %s", err)
			return err
		}

		eventResp := resp.GetEventResponse()
		if eventResp == nil {
			return errors.New("duplicate initial request received")
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			conn.notifyPublishResponse(stream.Context(), eventResp)
		}()
	}
}

func (s *streamer) Publish(ctx context.Context, msg *rtpubsub.SubscribedMessage) (bool, error) {
	s.lock.RLock()
	key := streamerKey(msg.PubSub, msg.Topic)
	conn, ok := s.subscribers[key]
	s.lock.RUnlock()
	if !ok {
		return false, nil
	}

	envelope, span, err := rtpubsub.GRPCEnvelopeFromSubscriptionMessage(ctx, msg, log, s.pubsub.tracingSpec)
	if err != nil {
		return true, err
	}

	ch, defFn := conn.registerPublishResponse(envelope.GetId())
	defer defFn()

	start := time.Now()
	err = conn.stream.Send(envelope)
	elapsed := diag.ElapsedSince(start)

	if span != nil {
		m := diag.ConstructSubscriptionSpanAttributes(envelope.GetTopic())
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromGRPCError(span, err)
		span.End()
	}

	if err != nil {
		err = fmt.Errorf("error returned from app while processing pub/sub event %v: %w", msg.CloudEvent[contribpubsub.IDField], rterrors.NewRetriable(err))
		log.Debug(err)
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
		return true, err
	}

	var resp *rtv1pb.SubscribeTopicEventsResponseAlpha1
	select {
	case <-ctx.Done():
		return true, ctx.Err()
	case resp = <-ch:
	}

	switch resp.GetStatus().GetStatus() {
	case rtv1pb.TopicEventResponse_SUCCESS: //nolint:nosnakecase
		// on uninitialized status, this is the case it defaults to as an uninitialized status defaults to 0 which is
		// success from protobuf definition
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Success)), "", msg.Topic, elapsed)
		return true, nil
	case rtv1pb.TopicEventResponse_RETRY: //nolint:nosnakecase
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
		// TODO: add retry error info
		return true, fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", msg.CloudEvent[contribpubsub.IDField], rterrors.NewRetriable(nil))
	case rtv1pb.TopicEventResponse_DROP: //nolint:nosnakecase
		log.Warnf("DROP status returned from app while processing pub/sub event %v", msg.CloudEvent[contribpubsub.IDField])
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Drop)), "", msg.Topic, elapsed)
		return true, rtpubsub.ErrMessageDropped
	default:
		// Consider unknown status field as error and retry
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
		return true, fmt.Errorf("unknown status returned from app while processing pub/sub event %v, status: %v, err: %w", msg.CloudEvent[contribpubsub.IDField], resp.GetStatus(), rterrors.NewRetriable(nil))
	}
}

func streamerKey(pubsub, topic string) string {
	return "___" + pubsub + "||" + topic
}
