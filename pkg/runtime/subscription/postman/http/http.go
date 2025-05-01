/*
Copyright 2025 The Dapr Authors
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

package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/dapr/components-contrib/contenttype"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman"
	"github.com/dapr/dapr/pkg/runtime/subscription/todo"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.processor.pubsub.subscription.http")

type Options struct {
	Tracing  *config.TracingSpec
	Channels *channels.Channels
	Adapter  pubsub.Adapter
}

type http struct {
	tracingSpec *config.TracingSpec
	channels    *channels.Channels
	adapter     pubsub.Adapter
}

func New(opts Options) postman.Interface {
	return &http{
		tracingSpec: opts.Tracing,
		channels:    opts.Channels,
		adapter:     opts.Adapter,
	}
}

func (h *http) Deliver(ctx context.Context, msg *pubsub.SubscribedMessage) error {
	cloudEvent := msg.CloudEvent

	var span trace.Span

	req := invokev1.NewInvokeMethodRequest(msg.Path).
		WithHTTPExtension(nethttp.MethodPost, "").
		WithRawDataBytes(msg.Data).
		WithContentType(contenttype.CloudEventContentType).
		WithCustomHTTPMetadata(msg.Metadata)
	defer req.Close()

	iTraceID := cloudEvent[contribpubsub.TraceParentField]
	if iTraceID == nil {
		iTraceID = cloudEvent[contribpubsub.TraceIDField]
	}
	if iTraceID != nil {
		traceID := iTraceID.(string)
		sc, _ := diag.SpanContextFromW3CString(traceID)
		ctx, span = diag.StartInternalCallbackSpan(ctx, "pubsub/"+msg.Topic, sc, h.tracingSpec)
	}

	start := time.Now()
	resp, err := h.channels.AppChannel().InvokeMethod(ctx, req, "")
	elapsed := diag.ElapsedSince(start)

	if err != nil {
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
		return fmt.Errorf("error returned from app channel while sending pub/sub event to app: %w", rterrors.NewRetriable(err))
	}
	defer resp.Close()

	statusCode := int(resp.Status().GetCode())

	if span != nil {
		m := diag.ConstructSubscriptionSpanAttributes(msg.Topic)
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromHTTPStatus(span, statusCode)
		span.End()
	}

	if (statusCode >= 200) && (statusCode <= 299) {
		// Any 2xx is considered a success.
		var appResponse contribpubsub.AppResponse
		err := json.NewDecoder(resp.RawData()).Decode(&appResponse)
		if err != nil {
			if errors.Is(err, io.EOF) {
				log.Debugf("skipping status check due to empty response body from pub/sub event %v", cloudEvent[contribpubsub.IDField])
			} else {
				log.Debugf("skipping status check due to error parsing result from pub/sub event %v: %s", cloudEvent[contribpubsub.IDField], err)
			}
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Success)), "", msg.Topic, elapsed)
			return nil
		}

		switch appResponse.Status {
		case "":
			// Consider empty status field as success
			fallthrough
		case contribpubsub.Success:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Success)), "", msg.Topic, elapsed)
			return nil
		case contribpubsub.Retry:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
			// TODO: add retry error info
			return fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", cloudEvent[contribpubsub.IDField], rterrors.NewRetriable(nil))
		case contribpubsub.Drop:
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Drop)), strings.ToLower(string(contribpubsub.Success)), msg.Topic, elapsed)
			log.Warnf("DROP status returned from app while processing pub/sub event %v", cloudEvent[contribpubsub.IDField])
			return pubsub.ErrMessageDropped
		}
		// Consider unknown status field as error and retry
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
		return fmt.Errorf("unknown status returned from app while processing pub/sub event %v, status: %v, err: %w", cloudEvent[contribpubsub.IDField], appResponse.Status, rterrors.NewRetriable(nil))
	}

	body, _ := resp.RawDataFull()
	if statusCode == nethttp.StatusNotFound {
		// These are errors that are not retriable, for now it is just 404 but more status codes can be added.
		// When adding/removing an error here, check if that is also applicable to GRPC since there is a mapping between HTTP and GRPC errors:
		// https://cloud.google.com/apis/design/errors#handling_errors
		log.Errorf("non-retriable error returned from app while processing pub/sub event %v: %s. status code returned: %v", cloudEvent[contribpubsub.IDField], body, statusCode)
		diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Drop)), "", msg.Topic, elapsed)
		return nil
	}

	// Every error from now on is a retriable error.
	errMsg := fmt.Sprintf("retriable error returned from app while processing pub/sub event %v, topic: %v, body: %s. status code returned: %v", cloudEvent[contribpubsub.IDField], cloudEvent[contribpubsub.TopicField], body, statusCode)
	log.Warnf(errMsg)
	diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, msg.PubSub, strings.ToLower(string(contribpubsub.Retry)), "", msg.Topic, elapsed)
	// return error status code for resiliency to decide on retry
	// TODO: Update types to uint32
	//nolint:gosec
	return resiliency.NewCodeError(int32(statusCode), rterrors.NewRetriable(errors.New(errMsg)))
}

// DeliverBulk publishes bulk message to a subscriber using HTTP and takes care
// of corresponding responses.
func (h *http) DeliverBulk(ctx context.Context, req *postman.DelivererBulkRequest) error {
	bscData := *req.BulkSubCallData
	psm := req.BulkSubMsg
	bsrr := req.BulkSubResiliencyRes
	bulkSubCallData := req.BulkSubCallData

	rawMsgEntries := make([]*pubsub.BulkSubscribeMessageItem, len(psm.PubSubMessages))
	entryRespReceived := make(map[string]bool, len(psm.PubSubMessages))
	for i, pubSubMsg := range psm.PubSubMessages {
		rawMsgEntries[i] = pubSubMsg.RawData
	}

	bsrr.Envelope[pubsub.Entries] = rawMsgEntries
	da, marshalErr := json.Marshal(&bsrr.Envelope)

	if marshalErr != nil {
		log.Errorf("Error serializing bulk cloud event in pubsub %s and topic %s: %s", psm.Pubsub, psm.Topic, marshalErr)
		if req.DeadLetterTopic != "" {
			entries := make([]contribpubsub.BulkMessageEntry, len(psm.PubSubMessages))
			for i, pubsubMsg := range psm.PubSubMessages {
				entries[i] = *pubsubMsg.Entry
			}
			bulkMsg := contribpubsub.BulkMessage{
				Entries:  entries,
				Topic:    psm.Topic,
				Metadata: psm.Metadata,
			}
			if dlqErr := h.sendBulkToDeadLetter(ctx, bulkSubCallData, &bulkMsg, req.DeadLetterTopic, true); dlqErr == nil {
				// dlq has been configured and message is successfully sent to dlq.
				for _, item := range rawMsgEntries {
					todo.AddBulkResponseEntry(&bsrr.Entries, item.EntryId, nil)
				}
				return nil
			}
		}
		bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)] += int64(len(rawMsgEntries))

		for _, item := range rawMsgEntries {
			todo.AddBulkResponseEntry(&bsrr.Entries, item.EntryId, marshalErr)
		}
		return marshalErr
	}

	spans := make([]trace.Span, len(rawMsgEntries))

	iReq := invokev1.NewInvokeMethodRequest(psm.Path).
		WithHTTPExtension(nethttp.MethodPost, "").
		WithRawDataBytes(da).
		WithContentType(contenttype.JSONContentType).
		WithCustomHTTPMetadata(psm.Metadata)
	defer iReq.Close()

	n := 0
	for _, pubsubMsg := range psm.PubSubMessages {
		cloudEvent := pubsubMsg.CloudEvent
		iTraceID := cloudEvent[contribpubsub.TraceParentField]
		if iTraceID == nil {
			iTraceID = cloudEvent[contribpubsub.TraceIDField]
		}
		if iTraceID != nil {
			traceID := iTraceID.(string)
			sc, _ := diag.SpanContextFromW3CString(traceID)
			var span trace.Span
			ctx, span = diag.StartInternalCallbackSpan(ctx, "pubsub/"+psm.Topic, sc, h.tracingSpec)
			if span != nil {
				spans[n] = span
				n++
			}
		}
	}
	spans = spans[:n]
	defer todo.EndSpans(spans)
	start := time.Now()
	resp, err := h.channels.AppChannel().InvokeMethod(ctx, iReq, "")
	elapsed := diag.ElapsedSince(start)
	if err != nil {
		bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)] += int64(len(rawMsgEntries))
		bscData.BulkSubDiag.Elapsed = elapsed
		todo.PopulateBulkSubscribeResponsesWithError(psm, &bsrr.Entries, err)
		return fmt.Errorf("error from app channel while sending pub/sub event to app: %w", err)
	}
	defer resp.Close()

	statusCode := int(resp.Status().GetCode())

	for _, span := range spans {
		m := diag.ConstructSubscriptionSpanAttributes(psm.Topic)
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromHTTPStatus(span, statusCode)
	}

	if (statusCode >= 200) && (statusCode <= 299) {
		// Any 2xx is considered a success.
		var appBulkResponse contribpubsub.AppBulkResponse
		err = json.NewDecoder(resp.RawData()).Decode(&appBulkResponse)
		if err != nil {
			bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)] += int64(len(rawMsgEntries))
			bscData.BulkSubDiag.Elapsed = elapsed
			todo.PopulateBulkSubscribeResponsesWithError(psm, &bsrr.Entries, err)
			return fmt.Errorf("failed unmarshalling app response for bulk subscribe: %w", err)
		}

		var hasAnyError bool
		for _, response := range appBulkResponse.AppResponses {
			if _, ok := (*bscData.EntryIdIndexMap)[response.EntryId]; ok {
				switch response.Status {
				case "":
					// When statusCode 2xx, Consider empty status field OR not receiving status for an item as retry
					fallthrough
				case contribpubsub.Retry:
					bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)]++
					entryRespReceived[response.EntryId] = true
					todo.AddBulkResponseEntry(&bsrr.Entries, response.EntryId,
						fmt.Errorf("RETRY required while processing bulk subscribe event for entry id: %v", response.EntryId))
					hasAnyError = true
				case contribpubsub.Success:
					bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Success)]++
					entryRespReceived[response.EntryId] = true
					todo.AddBulkResponseEntry(&bsrr.Entries, response.EntryId, nil)
				case contribpubsub.Drop:
					bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Drop)]++
					entryRespReceived[response.EntryId] = true
					log.Warnf("DROP status returned from app while processing pub/sub event %v", response.EntryId)
					todo.AddBulkResponseEntry(&bsrr.Entries, response.EntryId, nil)
					if req.DeadLetterTopic != "" {
						msg := psm.PubSubMessages[(*bscData.EntryIdIndexMap)[response.EntryId]]
						_ = h.sendToDeadLetter(ctx, bscData.PsName, &contribpubsub.NewMessage{
							Data:        msg.Entry.Event,
							Topic:       bscData.Topic,
							Metadata:    msg.Entry.Metadata,
							ContentType: &msg.Entry.ContentType,
						}, req.DeadLetterTopic)
					}
				default:
					// Consider unknown status field as error and retry
					bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)]++
					entryRespReceived[response.EntryId] = true
					todo.AddBulkResponseEntry(&bsrr.Entries, response.EntryId,
						fmt.Errorf("unknown status returned from app while processing bulk subscribe event %v: %v", response.EntryId, response.Status))
					hasAnyError = true
				}
			} else {
				log.Warnf("Invalid entry id received from app while processing pub/sub event %v", response.EntryId)
				continue
			}
		}
		for _, item := range rawMsgEntries {
			if !entryRespReceived[item.EntryId] {
				todo.AddBulkResponseEntry(&bsrr.Entries, item.EntryId,
					fmt.Errorf("Response not received, RETRY required while processing bulk subscribe event for entry id: %v", item.EntryId), //nolint:stylecheck
				)
				hasAnyError = true
				bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)]++
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

	if statusCode == nethttp.StatusNotFound {
		// These are errors that are not retriable, for now it is just 404 but more status codes can be added.
		// When adding/removing an error here, check if that is also applicable to GRPC since there is a mapping between HTTP and GRPC errors:
		// https://cloud.google.com/apis/design/errors#handling_errors
		log.Errorf("Non-retriable error returned from app while processing bulk pub/sub event. status code returned: %v", statusCode)
		bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Drop)] += int64(len(rawMsgEntries))
		bscData.BulkSubDiag.Elapsed = elapsed
		todo.PopulateBulkSubscribeResponsesWithError(psm, &bsrr.Entries, nil)
		return nil
	}

	// Every error from now on is a retriable error.
	retriableErrorStr := fmt.Sprintf("Retriable error returned from app while processing bulk pub/sub event, topic: %v. status code returned: %v", psm.Topic, statusCode)
	retriableError := errors.New(retriableErrorStr)
	log.Warn(retriableErrorStr)
	bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)] += int64(len(rawMsgEntries))
	bscData.BulkSubDiag.Elapsed = elapsed
	todo.PopulateBulkSubscribeResponsesWithError(psm, &bsrr.Entries, retriableError)
	return retriableError
}

// sendBulkToDeadLetter sends the bulk message to deadletter topic.
func (h *http) sendBulkToDeadLetter(ctx context.Context,
	bulkSubCallData *todo.BulkSubscribeCallData, msg *contribpubsub.BulkMessage, deadLetterTopic string,
	sendAllEntries bool,
) error {
	bscData := *bulkSubCallData
	data := make([]contribpubsub.BulkMessageEntry, len(msg.Entries))

	if sendAllEntries {
		data = msg.Entries
	} else {
		n := 0
		for _, message := range msg.Entries {
			entryId := (*bscData.EntryIdIndexMap)[message.EntryId] //nolint:stylecheck
			if (*bscData.BulkResponses)[entryId].Error != nil {
				data[n] = message
				n++
			}
		}
		data = data[:n]
	}
	bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Drop)] += int64(len(data))
	if bscData.BulkSubDiag.RetryReported {
		bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)] -= int64(len(data))
	}
	req := &contribpubsub.BulkPublishRequest{
		Entries:    data,
		PubsubName: bscData.PsName,
		Topic:      deadLetterTopic,
		Metadata:   msg.Metadata,
	}

	_, err := h.adapter.BulkPublish(ctx, req)
	if err != nil {
		log.Errorf("error sending message to dead letter, origin topic: %s dead letter topic %s err: %w", msg.Topic, deadLetterTopic, err)
	}

	return err
}

func (h *http) sendToDeadLetter(ctx context.Context, name string, msg *contribpubsub.NewMessage, deadLetterTopic string) error {
	req := &contribpubsub.PublishRequest{
		Data:        msg.Data,
		PubsubName:  name,
		Topic:       deadLetterTopic,
		Metadata:    msg.Metadata,
		ContentType: msg.ContentType,
	}

	if err := h.adapter.Publish(ctx, req); err != nil {
		log.Errorf("error sending message to dead letter, origin topic: %s dead letter topic %s err: %w", msg.Topic, deadLetterTopic, err)
		return err
	}

	return nil
}
