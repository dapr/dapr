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

package streamer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/kit/logger"
)

type Options struct {
	TracingSpec *config.TracingSpec
}

type Connections map[rtpubsub.ConnectionID]*conn

type Subscribers map[string]Connections

type streamer struct {
	tracingSpec      *config.TracingSpec
	subscribers      Subscribers
	subscribersIndex atomic.Uint64

	lock sync.RWMutex
}

var log = logger.NewLogger("dapr.runtime.pubsub.streamer")

func New(opts Options) rtpubsub.AdapterStreamer {
	return &streamer{
		tracingSpec: opts.TracingSpec,
		subscribers: make(Subscribers),
	}
}

func (s *streamer) Subscribe(stream rtv1pb.Dapr_SubscribeTopicEventsAlpha1Server, req *rtv1pb.SubscribeTopicEventsRequestInitialAlpha1, connectionID rtpubsub.ConnectionID) error {
	log.Warn("Lock Subscribe ConnectionID", connectionID)
	s.lock.Lock()
	key := s.StreamerKey(req.GetPubsubName(), req.GetTopic())

	connection := &conn{
		stream:            stream,
		publishResponses3: make(map[string]map[rtpubsub.ConnectionID]chan *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1),
		closeCh:           make(chan struct{}),
		connectionID:      connectionID,
	}
	if s.subscribers[key] == nil {
		s.subscribers[key] = make(Connections)
	}
	s.subscribers[key][connectionID] = connection

	log.Infof("Subscribing to pubsub '%s' topic '%s' ConnectionID%d", req.GetPubsubName(), req.GetTopic(), connectionID)
	s.lock.Unlock()
	log.Warn("Unlock Subscribe ConnectionID", connectionID)

	defer func() {
		log.Warn("Lock Subscribe defer")
		s.lock.Lock()
		if connections := s.subscribers[key]; connections != nil {
			delete(connections, connectionID)
			if len(connections) == 0 {
				delete(s.subscribers, key)
			}
		}
		s.lock.Unlock()
		log.Warn("Unlock Subscribe defer")
	}()

	errCh := make(chan error, 2)

	go func() {
		select {
		case <-connection.closeCh:
		case <-stream.Context().Done():
		}
		errCh <- nil
	}()

	go func() {
		for {
			log.Info("Waiting for Recv, connectionID", connectionID)
			resp, err := stream.Recv()
			log.Infof("Recv, connectionID%d, resp%s", connectionID, resp)
			s, ok := status.FromError(err)

			if (ok && s.Code() == codes.Canceled) ||
				errors.Is(err, context.Canceled) ||
				errors.Is(err, io.EOF) {
				log.Infof("Unsubscribed from pubsub '%s' topic '%s'", req.GetPubsubName(), req.GetTopic())
				errCh <- err
				return
			}

			if err != nil {
				log.Errorf("error receiving message from client stream: %s", err)
				errCh <- err
				return
			}

			eventResp := resp.GetEventProcessed()
			if eventResp == nil {
				errCh <- errors.New("duplicate initial request received")
				return
			}

			connection.notifyPublishResponse(eventResp)
		}
	}()

	// TODO: @joshvanl: add global wait group here to wait during shutdown.
	err := <-errCh
	go func() {
		if eerr := <-errCh; eerr != nil {
			log.Errorf("Error subscribing to pubsub '%s' topic '%s': %s", req.GetPubsubName(), req.GetTopic(), eerr)
		}
	}()
	return err
}

func (s *streamer) Publish(ctx context.Context, msg *rtpubsub.SubscribedMessage) error {
	log.Warn("RLock Publish ConnectionID", msg.SubscriberID)
	s.lock.RLock()
	key := s.StreamerKey(msg.PubSub, msg.Topic)
	connection, ok := s.subscribers[key][msg.SubscriberID]
	s.lock.RUnlock()
	log.Warn("RUnlock Publish ConnectionID", msg.SubscriberID)
	if !ok {
		return fmt.Errorf("no streamer subscribed to pubsub %q topic %q", msg.PubSub, msg.Topic)
	}

	if connection.closed.Load() {
		return errors.New("connection is closed")
	}

	envelope, span, err := rtpubsub.GRPCEnvelopeFromSubscriptionMessage(ctx, msg, log, s.tracingSpec)
	if err != nil {
		return err
	}

	ch, cleanup := connection.registerPublishResponse(envelope.GetId())
	if ch == nil {
		return fmt.Errorf("no client stream expecting publish response for id %s ConnectionID%d", envelope.GetId(), connection.connectionID)
	}
	defer cleanup()

	start := time.Now()
	log.Warn("Lock stream ConnectionID", connection.connectionID)
	connection.streamLock.Lock()
	err = connection.stream.Send(&rtv1pb.SubscribeTopicEventsResponseAlpha1{
		SubscribeTopicEventsResponseType: &rtv1pb.SubscribeTopicEventsResponseAlpha1_EventMessage{
			EventMessage: envelope,
		},
	})
	connection.streamLock.Unlock()
	log.Warn("Unlock stream ConnectionID", connection.connectionID)
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
		return err
	}

	var resp *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1
	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp = <-ch:
	}

	switch resp.GetStatus().GetStatus() {
	case rtv1pb.TopicEventResponse_SUCCESS: //nolint:nosnakecase
		// on uninitialized status, this is the case it defaults to as an uninitialized status defaults to 0 which is
		// success from protobuf definition
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Success)), "", msg.Topic, elapsed)
		return nil
	case rtv1pb.TopicEventResponse_RETRY: //nolint:nosnakecase
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
		// TODO: add retry error info
		return fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", msg.CloudEvent[contribpubsub.IDField], rterrors.NewRetriable(nil))
	case rtv1pb.TopicEventResponse_DROP: //nolint:nosnakecase
		log.Warnf("DROP status returned from app while processing pub/sub event %v", msg.CloudEvent[contribpubsub.IDField])
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Drop)), "", msg.Topic, elapsed)
		return rtpubsub.ErrMessageDropped
	default:
		// Consider unknown status field as error and retry
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
		return fmt.Errorf("unknown status returned from app while processing pub/sub event %v, status: %v, err: %w", msg.CloudEvent[contribpubsub.IDField], resp.GetStatus(), rterrors.NewRetriable(nil))
	}
}

func (s *streamer) StreamerKey(pubsub, topic string) string {
	return "___" + pubsub + "||" + topic
}

func (s *streamer) NextIndex() rtpubsub.ConnectionID {
	return rtpubsub.ConnectionID(s.subscribersIndex.Add(1))
}

func (s *streamer) Close(key string, connectionID rtpubsub.ConnectionID) {
	log.Warn("RLock Close")
	s.lock.RLock()
	defer func() {
		s.lock.RUnlock()
		log.Warn("Unlock Close defer")
	}()

	if conn, ok := s.subscribers[key][connectionID]; ok {
		if conn.closed.CompareAndSwap(false, true) {
			close(conn.closeCh)
		}
	}
}
