/*
Copyright 2025  The Dapr Authors
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

package grpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman"
	"github.com/dapr/dapr/pkg/runtime/subscription/todo"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.processor.pubsub.subscription.grpc")

type Options struct {
	Channel *manager.Manager
	Tracing *config.TracingSpec
	Adapter pubsub.Adapter
}

type grpc struct {
	channel     *manager.Manager
	tracingSpec *config.TracingSpec
	adapter     pubsub.Adapter
}

func New(opts Options) postman.Interface {
	return &grpc{
		channel:     opts.Channel,
		tracingSpec: opts.Tracing,
		adapter:     opts.Adapter,
	}
}

func (g *grpc) Deliver(ctx context.Context, msg *pubsub.SubscribedMessage) error {
	cloudEvent := msg.CloudEvent

	ctx, envelope, span, err := pubsub.GRPCEnvelopeFromSubscriptionMessage(ctx, msg, log, g.tracingSpec)
	if err != nil {
		return err
	}

	ctx = invokev1.WithCustomGRPCMetadata(ctx, msg.Metadata)
	ctx = g.channel.AddAppTokenToContext(ctx)

	conn, err := g.channel.GetAppClient()
	if err != nil {
		return fmt.Errorf("error while getting app client: %w", err)
	}
	clientV1 := rtv1.NewAppCallbackClient(conn)

	start := time.Now()
	res, err := clientV1.OnTopicEvent(ctx, envelope)
	elapsed := diag.ElapsedSince(start)

	if span != nil {
		m := diag.ConstructSubscriptionSpanAttributes(envelope.GetTopic())
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromGRPCError(span, err)
		span.End()
	}

	if err != nil {
		errStatus, hasErrStatus := status.FromError(err)
		if hasErrStatus && (errStatus.Code() == codes.Unimplemented) {
			// DROP
			log.Warnf("non-retriable error returned from app while processing pub/sub event %v: %s", cloudEvent[contribpubsub.IDField], err)
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Drop)), "", msg.Topic, elapsed)

			return nil
		}

		err = fmt.Errorf("error returned from app while processing pub/sub event %v: %w", cloudEvent[contribpubsub.IDField], rterrors.NewRetriable(err))
		log.Debug(err)
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)

		// return error status code for resiliency to decide on retry
		// TODO: Update types to uint32
		//nolint:gosec
		if hasErrStatus {
			return resiliency.NewCodeError(int32(errStatus.Code()), err)
		}

		// on error from application, return error for redelivery of event
		return err
	}

	switch res.GetStatus() {
	case rtv1.TopicEventResponse_SUCCESS: //nolint:nosnakecase
		// on uninitialized status, this is the case it defaults to as an uninitialized status defaults to 0 which is
		// success from protobuf definition
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Success)), "", msg.Topic, elapsed)
		return nil
	case rtv1.TopicEventResponse_RETRY: //nolint:nosnakecase
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
		// TODO: add retry error info
		return fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", cloudEvent[contribpubsub.IDField], rterrors.NewRetriable(nil))
	case rtv1.TopicEventResponse_DROP: //nolint:nosnakecase
		log.Warnf("DROP status returned from app while processing pub/sub event %v", cloudEvent[contribpubsub.IDField])
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Drop)), strings.ToLower(string(contribpubsub.Success)), msg.Topic, elapsed)

		return pubsub.ErrMessageDropped
	}

	// Consider unknown status field as error and retry
	diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
	return fmt.Errorf("unknown status returned from app while processing pub/sub event %v, status: %v, err: %w", cloudEvent[contribpubsub.IDField], res.GetStatus(), rterrors.NewRetriable(nil))
}

// DeliverBulk publishes bulk message to a subscriber using gRPC and takes care
// of corresponding responses.
func (g *grpc) DeliverBulk(ctx context.Context, req *postman.DeliverBulkRequest) error {
	bscData := *req.BulkSubCallData
	psm := req.BulkSubMsg
	bulkResponses := req.BulkResponses

	items := make([]*rtv1.TopicEventBulkRequestEntry, len(psm.PubSubMessages))
	entryRespReceived := make(map[string]bool, len(psm.PubSubMessages))
	for i, pubSubMsg := range psm.PubSubMessages {
		entry := pubSubMsg.Entry
		item, err := pubsub.FetchEntry(req.RawPayload, entry, psm.PubSubMessages[i].CloudEvent)
		if err != nil {
			bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)]++
			todo.AddBulkResponseEntry(bulkResponses, entry.EntryId, err)
			continue
		}
		items[i] = item
	}

	uuidObj, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("failed to generate UUID: %w", err)
	}
	envelope := &rtv1.TopicEventBulkRequest{
		Id:         uuidObj.String(),
		Entries:    items,
		Metadata:   psm.Metadata,
		Topic:      psm.Topic,
		PubsubName: psm.Pubsub,
		Type:       contribpubsub.DefaultBulkEventType,
		Path:       psm.Path,
	}

	spans := make([]trace.Span, len(psm.PubSubMessages))
	n := 0
	for _, pubSubMsg := range psm.PubSubMessages {
		cloudEvent := pubSubMsg.CloudEvent
		iTraceID := cloudEvent[contribpubsub.TraceParentField]
		if iTraceID == nil {
			iTraceID = cloudEvent[contribpubsub.TraceIDField]
		}
		if iTraceID != nil {
			if traceID, ok := iTraceID.(string); ok {
				sc, _ := diag.SpanContextFromW3CString(traceID)

				// no ops if trace is off
				var span trace.Span
				ctx, span = diag.StartInternalCallbackSpan(ctx, "pubsub/"+psm.Topic, sc, g.tracingSpec)
				if span != nil {
					ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
					spans[n] = span
					n++
				}
			} else {
				log.Warnf("ignored non-string traceid value: %v", iTraceID)
			}
		}
	}
	spans = spans[:n]
	defer todo.EndSpans(spans)
	ctx = invokev1.WithCustomGRPCMetadata(ctx, psm.Metadata)
	ctx = g.channel.AddAppTokenToContext(ctx)

	conn, err := g.channel.GetAppClient()
	if err != nil {
		return fmt.Errorf("error while getting app client: %w", err)
	}
	clientV1 := rtv1.NewAppCallbackAlphaClient(conn)

	start := time.Now()
	res, err := clientV1.OnBulkTopicEventAlpha1(ctx, envelope)
	elapsed := diag.ElapsedSince(start)

	for _, span := range spans {
		m := diag.ConstructSubscriptionSpanAttributes(envelope.GetTopic())
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromGRPCError(span, err)
	}

	if err != nil {
		errStatus, hasErrStatus := status.FromError(err)
		if hasErrStatus && (errStatus.Code() == codes.Unimplemented) {
			// DROP
			log.Warnf("non-retriable error returned from app while processing bulk pub/sub event: %s", err)
			bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Drop)] += int64(len(psm.PubSubMessages))
			bscData.BulkSubDiag.Elapsed = elapsed
			todo.PopulateBulkSubscribeResponsesWithError(psm, bulkResponses, nil)
			return nil
		}

		err = fmt.Errorf("error returned from app while processing bulk pub/sub event: %w", err)
		log.Debug(err)
		bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)] += int64(len(psm.PubSubMessages))
		bscData.BulkSubDiag.Elapsed = elapsed
		todo.PopulateBulkSubscribeResponsesWithError(psm, bulkResponses, err)

		// return error status code for resiliency to decide on retry
		if hasErrStatus {
			// TODO: Update types to uint32
			//nolint:gosec
			return resiliency.NewCodeError(int32(errStatus.Code()), err)
		}

		// on error from application, return error for redelivery of event
		return err
	}

	hasAnyError := false
	for _, response := range res.GetStatuses() {
		entryID := response.GetEntryId()
		if _, ok := (*bscData.EntryIdIndexMap)[entryID]; ok {
			switch response.GetStatus() {
			case rtv1.TopicEventResponse_SUCCESS: //nolint:nosnakecase
				// on uninitialized status, this is the case it defaults to as an uninitialized status defaults to 0 which is
				// success from protobuf definition
				bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Success)] += 1
				entryRespReceived[entryID] = true
				todo.AddBulkResponseEntry(bulkResponses, entryID, nil)
			case rtv1.TopicEventResponse_RETRY: //nolint:nosnakecase
				bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)] += 1
				entryRespReceived[entryID] = true
				todo.AddBulkResponseEntry(bulkResponses, entryID,
					fmt.Errorf("RETRY status returned from app while processing pub/sub event for entry id: %v", entryID))
				hasAnyError = true
			case rtv1.TopicEventResponse_DROP: //nolint:nosnakecase
				log.Warnf("DROP status returned from app while processing pub/sub event for entry id: %v", entryID)
				bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Drop)] += 1
				entryRespReceived[entryID] = true
				todo.AddBulkResponseEntry(bulkResponses, entryID, nil)
				if req.DeadLetterTopic != "" {
					msg := psm.PubSubMessages[(*bscData.EntryIdIndexMap)[entryID]]
					_ = g.sendToDeadLetter(ctx, bscData.PsName, &contribpubsub.NewMessage{
						Data:        msg.Entry.Event,
						Topic:       bscData.Topic,
						Metadata:    msg.Entry.Metadata,
						ContentType: &msg.Entry.ContentType,
					}, req.DeadLetterTopic)
				}
			default:
				// Consider unknown status field as error and retry
				bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)] += 1
				entryRespReceived[entryID] = true
				todo.AddBulkResponseEntry(bulkResponses, entryID,
					fmt.Errorf("unknown status returned from app while processing pub/sub event  for entry id %v: %v", entryID, response.GetStatus()))
				hasAnyError = true
			}
		} else {
			log.Warnf("Invalid entry id received from app while processing pub/sub event %v", entryID)
			continue
		}
	}
	for _, item := range psm.PubSubMessages {
		if !entryRespReceived[item.Entry.EntryId] {
			todo.AddBulkResponseEntry(bulkResponses, item.Entry.EntryId,
				fmt.Errorf("Response not received, RETRY required while processing bulk subscribe event for entry id: %v", item.Entry.EntryId), //nolint:stylecheck
			)
			hasAnyError = true
			bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)] += 1
		}
	}
	bscData.BulkSubDiag.Elapsed = elapsed
	if hasAnyError {
		//nolint:stylecheck
		return errors.New("Few message(s) have failed during bulk subscribe operation")
	} else {
		return nil
	}
}

func (g *grpc) sendToDeadLetter(ctx context.Context, name string, msg *contribpubsub.NewMessage, deadLetterTopic string) error {
	req := &contribpubsub.PublishRequest{
		Data:        msg.Data,
		PubsubName:  name,
		Topic:       deadLetterTopic,
		Metadata:    msg.Metadata,
		ContentType: msg.ContentType,
	}

	if err := g.adapter.Publish(ctx, req); err != nil {
		log.Errorf("error sending message to dead letter, origin topic: %s dead letter topic %s err: %w", msg.Topic, deadLetterTopic, err)
		return err
	}

	return nil
}
