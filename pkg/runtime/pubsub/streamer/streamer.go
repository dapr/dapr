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

type streamer struct {
	tracingSpec *config.TracingSpec
	subscribers map[string][]*conn
	lock        sync.RWMutex
}

var log = logger.NewLogger("dapr.runtime.pubsub.streamer")

func New(opts Options) rtpubsub.AdapterStreamer {
	return &streamer{
		tracingSpec: opts.TracingSpec,
		subscribers: make(map[string][]*conn),
	}
}

func (s *streamer) Subscribe(stream rtv1pb.Dapr_SubscribeTopicEventsAlpha1Server, req *rtv1pb.SubscribeTopicEventsRequestInitialAlpha1) error {
	s.lock.Lock()
	key := s.StreamerKey(req.GetPubsubName(), req.GetTopic())

	connection := &conn{
		stream:           stream,
		publishResponses: make(map[string]chan *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1),
	}

	if s.subscribers[key] == nil {
		s.subscribers[key] = make([]*conn, 0)
	}
	s.subscribers[key] = append(s.subscribers[key], connection)
	s.lock.Unlock()

	log.Infof("Subscribing to pubsub '%s' topic '%s'", req.GetPubsubName(), req.GetTopic())

	defer func() {
		s.lock.Lock()

		conns := s.subscribers[key]
		for i, c := range conns {
			if c == connection {
				s.subscribers[key] = append(conns[:i], conns[i+1:]...)
				break
			}
		}

		if len(s.subscribers[key]) == 0 {
			delete(s.subscribers, key)
		}
		s.lock.Unlock()
	}()

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		resp, err := stream.Recv()

		s, ok := status.FromError(err)

		if (ok && s.Code() == codes.Canceled) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, io.EOF) {
			log.Infof("Unsubscribed from pubsub '%s' topic '%s'", req.GetPubsubName(), req.GetTopic())
			return err
		}

		if err != nil {
			log.Errorf("error receiving message from client stream: %s", err)
			return err
		}

		eventResp := resp.GetEventProcessed()
		if eventResp == nil {
			return errors.New("duplicate initial request received")
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			connection.notifyPublishResponse(stream.Context(), eventResp)
		}()
	}
}

func (s *streamer) Publish(ctx context.Context, msg *rtpubsub.SubscribedMessage) error {
	s.lock.RLock()
	key := s.StreamerKey(msg.PubSub, msg.Topic)
	conns, ok := s.subscribers[key]
	s.lock.RUnlock()

	if !ok || len(conns) == 0 {
		return fmt.Errorf("no streamer subscribed to pubsub %q topic %q", msg.PubSub, msg.Topic)
	}

	envelope, span, err := rtpubsub.GRPCEnvelopeFromSubscriptionMessage(ctx, msg, log, s.tracingSpec)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(conns))

	for _, connection := range conns {
		wg.Add(1)
		go func(conn *conn) {
			defer wg.Done()

			ch, defFn := conn.registerPublishResponse(envelope.GetId())
			defer defFn()

			start := time.Now()
			conn.streamLock.Lock()
			err := conn.stream.Send(&rtv1pb.SubscribeTopicEventsResponseAlpha1{
				SubscribeTopicEventsResponseType: &rtv1pb.SubscribeTopicEventsResponseAlpha1_EventMessage{
					EventMessage: envelope,
				},
			})
			conn.streamLock.Unlock()

			if err != nil {
				errCh <- fmt.Errorf("send error: %w", err)
				return
			}

			if span != nil {
				m := diag.ConstructSubscriptionSpanAttributes(envelope.GetTopic())
				diag.AddAttributesToSpan(span, m)
				diag.UpdateSpanStatusFromGRPCError(span, err)
				span.End()
			}

			var resp *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1
			select {
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			case resp = <-ch:
			}

			switch resp.GetStatus().GetStatus() {
			case rtv1pb.TopicEventResponse_SUCCESS: //nolint:nosnakecase
				// on uninitialized status, this is the case it defaults to as an uninitialized status defaults to 0 which is
				// success from protobuf definition
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Success)), "", msg.Topic, diag.ElapsedSince(start))
				return
			case rtv1pb.TopicEventResponse_RETRY: //nolint:nosnakecase
				// TODO: add retry error info
				errCh <- fmt.Errorf("RETRY status from subscriber: %w", rterrors.NewRetriable(nil))
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, diag.ElapsedSince(start))
			case rtv1pb.TopicEventResponse_DROP: //nolint:nosnakecase
				log.Warnf("DROP status returned from app while processing pub/sub event %v", msg.CloudEvent[contribpubsub.IDField])
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Drop)), "", msg.Topic, diag.ElapsedSince(start))
				errCh <- rtpubsub.ErrMessageDropped
			default:
				// Consider unknown status field as error and retry
				errCh <- fmt.Errorf("unknown status: %v", resp.GetStatus())
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, diag.ElapsedSince(start))
			}
		}(connection)
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("publish errors (%d): %v", len(errs), errs)
	}
	return nil
}

func (s *streamer) StreamerKey(pubsub, topic string) string {
	return "___" + pubsub + "||" + topic
}
