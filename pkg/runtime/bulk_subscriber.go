package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/contenttype"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

type pubsubBulkSubscribedMessage struct {
	cloudEvents []map[string]interface{}
	rawData     []runtimePubsub.BulkSubscribeMessageItem
	entries     []*pubsub.BulkMessageEntry
	data        []byte
	topic       string
	metadata    map[string]string
	pubsub      string
	path        string
	length      int
}

type bulkSubIngressDiagnostics struct {
	statusWiseDiag map[string]int64
	elapsed        float64
	retryReported  bool
}

// bulkSubscribeTopic subscribes to a topic for bulk messages and invokes subscriber app endpoint(s).

// Major steps inside a bulk handler:
//  1. Deserialize pubsub metadata and determine if rawPayload or not
//     1.A. If any error occurs, send to DLQ if configured, else send back error for all messages
//  2. Iterate through each message and validate entryId is NOT blank
//     2.A. If it is a raw payload:
//     2.A.i. Get route path, if processable
//     2.A.ii. If route path is non-blank, generate base64 encoding of event data
//     and set contentType, if provided, else set to "application/octet-stream"
//     2.A.iii. Finally, form a child message to be sent to app and add it to the list of messages,
//     to be sent to app (this list of messages is registered against correct path in an internal map)
//     2.B. If it is NOT a raw payload (it is considered a cloud event):
//     2.B.i. Unmarshal it into a map[string]interface{}
//     2.B.ii. If any error while unmarshalling, send to DLQ if configured, else register error for this message
//     2.B.iii. Check if message expired
//     2.B.iv. Get route path, if processable
//     2.B.v. If route path is non-blank, form a child message to be sent to app and add it to the list of messages,
//  3. Iterate through map prepared for path vs list of messages to be sent on this path
//     3.A. Prepare envelope for the list of messages to be sent to app on this path
//     3.B. Send the envelope to app by invoking http endpoint
//  4. Check if any error has occurred so far in processing for any of the message and invoke DLQ, if configured.
//  5. Send back responses array to broker interface.
func (a *DaprRuntime) bulkSubscribeTopic(ctx context.Context, policy resiliency.Runner[any],
	psName string, topic string, route TopicRouteElem, namespacedConsumer bool,
) error {
	ps, ok := a.pubSubs[psName]
	if !ok {
		return runtimePubsub.NotFoundError{PubsubName: psName}
	}

	subscribeTopic := topic
	if namespacedConsumer {
		subscribeTopic = a.namespace + topic
	}

	req := pubsub.SubscribeRequest{
		Topic:    subscribeTopic,
		Metadata: route.metadata,
	}

	bulkHandler := func(ctx context.Context, msg *pubsub.BulkMessage) ([]pubsub.BulkSubscribeResponseEntry, error) {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]string, 1)
		}

		msg.Metadata[pubsubName] = psName
		bulkSubDiag := newBulkSubIngressDiagnostics()
		bulkResponses := make([]pubsub.BulkSubscribeResponseEntry, len(msg.Entries))
		rawPayload, err := contribMetadata.IsRawPayload(route.metadata)
		if err != nil {
			log.Errorf("error deserializing pubsub metadata: %s", err)
			if dlqErr := a.sendBulkToDLQIfConfigured(ctx, psName, msg, route, nil, nil, &bulkSubDiag); dlqErr != nil {
				populateAllBulkResponsesWithError(msg, &bulkResponses, err)
				reportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
				return bulkResponses, err
			}
			reportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
			return nil, nil
		}
		routePathBulkMessageMap := make(map[string]pubsubBulkSubscribedMessage)
		entryIdIndexMap := make(map[string]int, len(msg.Entries)) //nolint:stylecheck
		hasAnyError := false
		for i, message := range msg.Entries {
			if entryIdErr := validateEntryId(message.EntryId, i); entryIdErr != nil { //nolint:stylecheck
				bulkResponses[i].Error = entryIdErr
				hasAnyError = true
				continue
			}
			entryIdIndexMap[message.EntryId] = i
			if rawPayload {
				rPath, routeErr := a.getRouteIfProcessable(ctx, route, &(msg.Entries[i]), i, &bulkResponses, string(message.Event), psName, topic, &bulkSubDiag)
				if routeErr != nil {
					hasAnyError = true
					continue
				}
				// For grpc, we can still send the entry even if path is blank, App can take a decision
				if rPath == "" && a.runtimeConfig.ApplicationProtocol == HTTPProtocol {
					continue
				}
				dataB64 := base64.StdEncoding.EncodeToString(message.Event)
				var contenttype string
				if message.ContentType != "" {
					contenttype = message.ContentType
				} else {
					contenttype = "application/octet-stream"
				}
				populateBulkSubcribedMessage(&(msg.Entries[i]), dataB64, &routePathBulkMessageMap, rPath, i, msg, false, psName, contenttype, namespacedConsumer, a.namespace)
			} else {
				var cloudEvent map[string]interface{}
				err = json.Unmarshal(message.Event, &cloudEvent)
				if err != nil {
					log.Errorf("error deserializing one of the messages in bulk cloud event in pubsub %s and topic %s: %s", psName, topic, err)
					bulkResponses[i].Error = err
					bulkResponses[i].EntryId = message.EntryId
					hasAnyError = true
					continue
				}
				if pubsub.HasExpired(cloudEvent) {
					log.Warnf("dropping expired pub/sub event %v as of %v", cloudEvent[pubsub.IDField], cloudEvent[pubsub.ExpirationField])
					bulkSubDiag.statusWiseDiag[string(pubsub.Drop)]++
					if route.deadLetterTopic != "" {
						_ = a.sendToDeadLetter(psName, &pubsub.NewMessage{
							Data:        message.Event,
							Topic:       topic,
							Metadata:    message.Metadata,
							ContentType: &message.ContentType,
						}, route.deadLetterTopic)
					}
					bulkResponses[i].EntryId = message.EntryId
					bulkResponses[i].Error = nil
					continue
				}
				rPath, routeErr := a.getRouteIfProcessable(ctx, route, &(msg.Entries[i]), i, &bulkResponses, cloudEvent, psName, topic, &bulkSubDiag)
				if routeErr != nil {
					hasAnyError = true
					continue
				}
				// For grpc, we can still send the entry even if path is blank, App can take a decision
				if rPath == "" && a.runtimeConfig.ApplicationProtocol == HTTPProtocol {
					continue
				}
				populateBulkSubcribedMessage(&(msg.Entries[i]), cloudEvent, &routePathBulkMessageMap, rPath, i, msg, true, psName, message.ContentType, namespacedConsumer, a.namespace)
			}
		}
		var overallInvokeErr error
		for path, psm := range routePathBulkMessageMap {
			invokeErr := a.createEnvelopeAndInvokeSubscriber(ctx, psm, topic, psName, msg, route, &bulkResponses, &entryIdIndexMap, path, policy, &bulkSubDiag, rawPayload)
			if invokeErr != nil {
				hasAnyError = true
				err = invokeErr
				overallInvokeErr = invokeErr
			}
		}
		if errors.Is(overallInvokeErr, context.Canceled) {
			reportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
			return bulkResponses, overallInvokeErr
		}
		if hasAnyError {
			// Sending msg to dead letter queue.
			// If no DLQ is configured, return error for backwards compatibility (component-level retry).
			bulkSubDiag.retryReported = true
			if dlqErr := a.sendBulkToDLQIfConfigured(ctx, psName, msg, route, &entryIdIndexMap, &bulkResponses, &bulkSubDiag); dlqErr != nil {
				reportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
				return bulkResponses, err
			}
			reportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
			return nil, nil
		}
		reportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
		return bulkResponses, err
	}

	if bulkSubscriber, ok := ps.component.(pubsub.BulkSubscriber); ok {
		return bulkSubscriber.BulkSubscribe(ctx, req, bulkHandler)
	}

	return runtimePubsub.NewDefaultBulkSubscriber(ps.component).BulkSubscribe(ctx, req, bulkHandler)
}

// sendBulkToDLQIfConfigured sends the message to the dead letter queue if configured.
func (a *DaprRuntime) sendBulkToDLQIfConfigured(ctx context.Context, psName string, msg *pubsub.BulkMessage, route TopicRouteElem,
	entryIdIndexMap *map[string]int, bulkResponses *[]pubsub.BulkSubscribeResponseEntry, bulkSubDiag *bulkSubIngressDiagnostics, //nolint:stylecheck
) error {
	if route.deadLetterTopic != "" {
		if dlqErr := a.sendBulkToDeadLetter(ctx, psName, msg, route.deadLetterTopic, nil, nil, bulkSubDiag); dlqErr == nil {
			// dlq has been configured and whole bulk of messages is successfully sent to dlq.
			return nil
		}
	}
	if !bulkSubDiag.retryReported {
		bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += int64(len(msg.Entries))
	}
	return errors.New("failed to send to DLQ as DLQ was not configured")
}

// getRouteIfProcessable returns the route path if the message is processable.
func (a *DaprRuntime) getRouteIfProcessable(ctx context.Context, route TopicRouteElem, message *pubsub.BulkMessageEntry,
	i int, bulkResponses *[]pubsub.BulkSubscribeResponseEntry, matchElem interface{},
	psName string, topic string, bulkSubDiag *bulkSubIngressDiagnostics,
) (string, error) {
	rPath, shouldProcess, routeErr := findMatchingRoute(route.rules, matchElem)
	if routeErr != nil {
		log.Errorf("error finding matching route for event in bulk subscribe %s and topic %s for entry id %s: %s", psName, topic, message.EntryId, routeErr)
		setBulkResponseEntry(bulkResponses, i, message.EntryId, routeErr)
		return "", routeErr
	}
	if !shouldProcess {
		// The event does not match any route specified so ignore it.
		log.Warnf("no matching route for event in pubsub %s and topic %s; skipping", psName, topic)
		bulkSubDiag.statusWiseDiag[string(pubsub.Drop)]++
		if route.deadLetterTopic != "" {
			_ = a.sendToDeadLetter(psName, &pubsub.NewMessage{
				Data:        message.Event,
				Topic:       topic,
				Metadata:    message.Metadata,
				ContentType: &message.ContentType,
			}, route.deadLetterTopic)
		}
		setBulkResponseEntry(bulkResponses, i, message.EntryId, nil)
		return "", nil
	}
	return rPath, nil
}

// createEnvelopeAndInvokeSubscriber creates the envelope and invokes the subscriber.
func (a *DaprRuntime) createEnvelopeAndInvokeSubscriber(ctx context.Context, psm pubsubBulkSubscribedMessage, topic string, psName string,
	msg *pubsub.BulkMessage, route TopicRouteElem, bulkResponses *[]pubsub.BulkSubscribeResponseEntry,
	entryIdIndexMap *map[string]int, path string, policy resiliency.Runner[any], bulkSubDiag *bulkSubIngressDiagnostics, //nolint:stylecheck
	rawPayload bool,
) error {
	var id string
	idObj, err := uuid.NewRandom()
	if err != nil {
		id = idObj.String()
	}
	psm.cloudEvents = psm.cloudEvents[:psm.length]
	psm.rawData = psm.rawData[:psm.length]
	psm.entries = psm.entries[:psm.length]
	envelope := runtimePubsub.NewBulkSubscribeEnvelope(&runtimePubsub.BulkSubscribeEnvelope{
		ID:       id,
		Topic:    topic,
		Entries:  psm.rawData,
		Pubsub:   psName,
		Metadata: msg.Metadata,
	})
	da, marshalErr := json.Marshal(&envelope)
	if marshalErr != nil {
		log.Errorf("error serializing bulk cloud event in pubsub %s and topic %s: %s", psName, msg.Topic, marshalErr)
		if route.deadLetterTopic != "" {
			entries := make([]pubsub.BulkMessageEntry, len(psm.entries))
			for i, entry := range psm.entries {
				entries[i] = *entry
			}
			bulkMsg := pubsub.BulkMessage{
				Entries:  entries,
				Topic:    msg.Topic,
				Metadata: msg.Metadata,
			}
			if dlqErr := a.sendBulkToDeadLetter(ctx, psName, &bulkMsg, route.deadLetterTopic, entryIdIndexMap, nil, bulkSubDiag); dlqErr == nil {
				// dlq has been configured and message is successfully sent to dlq.
				for _, item := range psm.entries {
					ind := (*entryIdIndexMap)[item.EntryId]
					setBulkResponseEntry(bulkResponses, ind, item.EntryId, nil)
				}
				return nil
			}
		}
		bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += int64(len(psm.entries))

		for _, item := range psm.entries {
			ind := (*entryIdIndexMap)[item.EntryId]
			setBulkResponseEntry(bulkResponses, ind, item.EntryId, marshalErr)
		}
		return marshalErr
	}
	psm.data = da
	psm.path = path
	_, err = policy(func(ctx context.Context) (any, error) {
		var pErr error
		switch a.runtimeConfig.ApplicationProtocol {
		case HTTPProtocol:
			pErr = a.publishBulkMessageHTTP(ctx, &psm, bulkResponses, *entryIdIndexMap, bulkSubDiag)
		case GRPCProtocol:
			pErr = a.publishBulkMessageGRPC(ctx, &psm, bulkResponses, *entryIdIndexMap, bulkSubDiag, rawPayload)
		default:
			pErr = backoff.Permanent(errors.New("invalid application protocol"))
		}
		return nil, pErr
	})
	return err
}

// publishBulkMessageHTTP publishes bulk message to a subscriber using HTTP and takes care of corresponding responses.
func (a *DaprRuntime) publishBulkMessageHTTP(ctx context.Context, msg *pubsubBulkSubscribedMessage,
	bulkResponses *[]pubsub.BulkSubscribeResponseEntry, entryIdIndexMap map[string]int, bulkSubDiag *bulkSubIngressDiagnostics, //nolint:stylecheck
) error {
	spans := make([]trace.Span, len(msg.entries))

	req := invokev1.NewInvokeMethodRequest(msg.path).
		WithHTTPExtension(nethttp.MethodPost, "").
		WithRawDataBytes(msg.data).
		WithContentType(contenttype.CloudEventContentType).
		WithCustomHTTPMetadata(msg.metadata)
	defer req.Close()

	n := 0
	for _, cloudEvent := range msg.cloudEvents {
		if cloudEvent[pubsub.TraceIDField] != nil {
			traceID := cloudEvent[pubsub.TraceIDField].(string)
			sc, _ := diag.SpanContextFromW3CString(traceID)
			spanName := fmt.Sprintf("pubsub/%s", msg.topic)
			var span trace.Span
			ctx, span = diag.StartInternalCallbackSpan(ctx, spanName, sc, a.globalConfig.Spec.TracingSpec)
			spans[n] = span
			n++
		}
	}
	spans = spans[:n]
	defer endSpans(spans)
	start := time.Now()
	resp, err := a.appChannel.InvokeMethod(ctx, req)
	elapsed := diag.ElapsedSince(start)
	if err != nil {
		bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += int64(len(msg.entries))
		bulkSubDiag.elapsed = elapsed
		populateBulkSubscribeResponsesWithError(msg.entries, bulkResponses, &entryIdIndexMap, err)
		return fmt.Errorf("error from app channel while sending pub/sub event to app: %w", err)
	}
	defer resp.Close()

	statusCode := int(resp.Status().Code)

	for _, span := range spans {
		if span != nil {
			m := diag.ConstructSubscriptionSpanAttributes(msg.topic)
			diag.AddAttributesToSpan(span, m)
			diag.UpdateSpanStatusFromHTTPStatus(span, statusCode)
		}
	}

	if (statusCode >= 200) && (statusCode <= 299) {
		// Any 2xx is considered a success.
		var appBulkResponse pubsub.AppBulkResponse
		err := json.NewDecoder(resp.RawData()).Decode(&appBulkResponse)
		if err != nil {
			bulkSubDiag.statusWiseDiag[string(pubsub.Success)] += int64(len(msg.entries))
			bulkSubDiag.elapsed = elapsed
			populateBulkSubscribeResponsesWithError(msg.entries, bulkResponses, &entryIdIndexMap, err)
			return errors.Wrap(err, "failed unmarshalling app response for bulk subscribe")
		}

		var hasAnyError bool
		for _, response := range appBulkResponse.AppResponses {
			if entryId, ok := entryIdIndexMap[response.EntryId]; ok { //nolint:stylecheck
				switch response.Status {
				case "":
					// When statusCode 2xx, Consider empty status field OR not receiving status for an item as retry
					fallthrough
				case pubsub.Retry:
					bulkSubDiag.statusWiseDiag[string(pubsub.Retry)]++
					setBulkResponseEntry(bulkResponses, entryId, response.EntryId,
						errors.Errorf("RETRY required while processing bulk subscribe event for entry id: %v", response.EntryId))
					hasAnyError = true
				case pubsub.Success:
					bulkSubDiag.statusWiseDiag[string(pubsub.Success)]++
					setBulkResponseEntry(bulkResponses, entryId, response.EntryId, nil)
				case pubsub.Drop:
					bulkSubDiag.statusWiseDiag[string(pubsub.Drop)]++
					log.Warnf("DROP status returned from app while processing pub/sub event %v", response.EntryId)
					setBulkResponseEntry(bulkResponses, entryId, response.EntryId, nil)
				default:
					// Consider unknown status field as error and retry
					bulkSubDiag.statusWiseDiag[string(pubsub.Retry)]++
					setBulkResponseEntry(bulkResponses, entryId, response.EntryId,
						errors.Errorf("unknown status returned from app while processing bulk subscribe event %v: %v", response.EntryId, response.Status))
					hasAnyError = true
				}
			} else {
				log.Warnf("Invalid entry id received from app while processing pub/sub event %v", response.EntryId)
				continue
			}
		}
		for _, item := range msg.entries {
			ind := entryIdIndexMap[item.EntryId]
			if (*bulkResponses)[ind].EntryId == "" {
				setBulkResponseEntry(bulkResponses, ind, item.EntryId,
					errors.Errorf("Response not received, RETRY required while processing bulk subscribe event for entry id: %v", item.EntryId))
				hasAnyError = true
				bulkSubDiag.statusWiseDiag[string(pubsub.Retry)]++
			}
		}
		bulkSubDiag.elapsed = elapsed
		if hasAnyError {
			return errors.New("Few message(s) have failed during bulk subscribe operation")
		} else {
			return nil
		}
	}

	if statusCode == nethttp.StatusNotFound {
		// These are errors that are not retriable, for now it is just 404 but more status codes can be added.
		// When adding/removing an error here, check if that is also applicable to GRPC since there is a mapping between HTTP and GRPC errors:
		// https://cloud.google.com/apis/design/errors#handling_errors
		log.Errorf("non-retriable error returned from app while processing bulk pub/sub event. status code returned: %v", statusCode)
		bulkSubDiag.statusWiseDiag[string(pubsub.Drop)] += int64(len(msg.entries))
		bulkSubDiag.elapsed = elapsed
		populateBulkSubscribeResponsesWithError(msg.entries, bulkResponses, &entryIdIndexMap, nil)
		return nil
	}

	// Every error from now on is a retriable error.
	log.Warnf("retriable error returned from app while processing bulk pub/sub event, topic: %v. status code returned: %v", msg.topic, statusCode)
	bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += int64(len(msg.entries))
	bulkSubDiag.elapsed = elapsed
	populateBulkSubscribeResponsesWithError(msg.entries, bulkResponses, &entryIdIndexMap, errors.Errorf("retriable error returned from app while processing bulk pub/sub event, topic: %v. status code returned: %v", msg.topic, statusCode))
	return errors.Errorf("retriable error returned from app while processing bulk pub/sub event, topic: %v. status code returned: %v", msg.topic, statusCode)
}

func extractCloudEvent(event map[string]interface{}) (runtimev1pb.TopicEventBulkRequestEntry_CloudEvent, error) { //nolint:nosnakecase
	envelope := &runtimev1pb.TopicEventCERequest{
		Id:              extractCloudEventProperty(event, pubsub.IDField),
		Source:          extractCloudEventProperty(event, pubsub.SourceField),
		DataContentType: extractCloudEventProperty(event, pubsub.DataContentTypeField),
		Type:            extractCloudEventProperty(event, pubsub.TypeField),
		SpecVersion:     extractCloudEventProperty(event, pubsub.SpecVersionField),
	}

	if data, ok := event[pubsub.DataField]; ok && data != nil {
		envelope.Data = nil

		if contenttype.IsStringContentType(envelope.DataContentType) {
			switch v := data.(type) {
			case string:
				envelope.Data = []byte(v)
			case []byte:
				envelope.Data = v
			default:
				return runtimev1pb.TopicEventBulkRequestEntry_CloudEvent{}, ErrUnexpectedEnvelopeData //nolint:nosnakecase
			}
		} else if contenttype.IsJSONContentType(envelope.DataContentType) || contenttype.IsCloudEventContentType(envelope.DataContentType) {
			envelope.Data, _ = json.Marshal(data)
		}
	}
	extensions, extensionsErr := extractCloudEventExtensions(event)
	if extensionsErr != nil {
		return runtimev1pb.TopicEventBulkRequestEntry_CloudEvent{}, extensionsErr
	}
	envelope.Extensions = extensions
	return runtimev1pb.TopicEventBulkRequestEntry_CloudEvent{CloudEvent: envelope}, nil //nolint:nosnakecase
}

func fetchEntry(rawPayload bool, entry *pubsub.BulkMessageEntry, cloudEvent map[string]interface{}) (*runtimev1pb.TopicEventBulkRequestEntry, error) {
	if rawPayload {
		return &runtimev1pb.TopicEventBulkRequestEntry{
			EntryId:     entry.EntryId,
			Event:       &runtimev1pb.TopicEventBulkRequestEntry_Bytes{Bytes: entry.Event}, //nolint:nosnakecase
			ContentType: entry.ContentType,
			Metadata:    entry.Metadata,
		}, nil
	} else {
		eventLocal, err := extractCloudEvent(cloudEvent)
		if err != nil {
			return nil, err
		}
		return &runtimev1pb.TopicEventBulkRequestEntry{
			EntryId:     entry.EntryId,
			Event:       &eventLocal,
			ContentType: entry.ContentType,
			Metadata:    entry.Metadata,
		}, nil
	}
}

// publishBulkMessageGRPC publishes bulk message to a subscriber using gRPC and takes care of corresponding responses.
func (a *DaprRuntime) publishBulkMessageGRPC(ctx context.Context, msg *pubsubBulkSubscribedMessage,
	bulkResponses *[]pubsub.BulkSubscribeResponseEntry, entryIdIndexMap map[string]int, bulkSubDiag *bulkSubIngressDiagnostics, //nolint:stylecheck
	rawPayload bool,
) error {
	items := make([]*runtimev1pb.TopicEventBulkRequestEntry, len(msg.entries))
	for i, entry := range msg.entries {
		item, err := fetchEntry(rawPayload, entry, msg.cloudEvents[i])
		if err != nil {
			bulkSubDiag.statusWiseDiag[string(pubsub.Retry)]++
			setBulkResponseEntry(bulkResponses, i, entry.EntryId, err)
			continue
		}
		items[i] = item
	}

	envelope := &runtimev1pb.TopicEventBulkRequest{
		Id:         uuid.New().String(),
		Entries:    items,
		Metadata:   msg.metadata,
		Topic:      msg.topic,
		PubsubName: msg.pubsub,
		Type:       pubsub.DefaultBulkEventType,
		Path:       msg.path,
	}

	spans := make([]trace.Span, len(msg.entries))
	n := 0
	for _, cloudEvent := range msg.cloudEvents {
		if iTraceID, ok := cloudEvent[pubsub.TraceIDField]; ok {
			if traceID, ok := iTraceID.(string); ok {
				sc, _ := diag.SpanContextFromW3CString(traceID)

				// no ops if trace is off
				var span trace.Span
				ctx, span = diag.StartInternalCallbackSpan(ctx, "pubsub/"+msg.topic, sc, a.globalConfig.Spec.TracingSpec)
				ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
				spans[n] = span
				n++
			} else {
				log.Warnf("ignored non-string traceid value: %v", iTraceID)
			}
		}
	}
	spans = spans[:n]
	defer endSpans(spans)
	ctx = invokev1.WithCustomGRPCMetadata(ctx, msg.metadata)

	conn, err := a.grpc.GetAppClient()
	if err != nil {
		return fmt.Errorf("error while getting app client: %w", err)
	}
	clientV1 := runtimev1pb.NewAppCallbackAlphaClient(conn)

	start := time.Now()
	res, err := clientV1.OnBulkTopicEventAlpha1(ctx, envelope)
	elapsed := diag.ElapsedSince(start)

	for _, span := range spans {
		if span != nil {
			m := diag.ConstructSubscriptionSpanAttributes(envelope.Topic)
			diag.AddAttributesToSpan(span, m)
			diag.UpdateSpanStatusFromGRPCError(span, err)
		}
	}

	if err != nil {
		errStatus, hasErrStatus := status.FromError(err)
		if hasErrStatus && (errStatus.Code() == codes.Unimplemented) {
			// DROP
			log.Warnf("non-retriable error returned from app while processing bulk pub/sub event: %s", err)
			bulkSubDiag.statusWiseDiag[string(pubsub.Drop)] += int64(len(msg.entries))
			bulkSubDiag.elapsed = elapsed
			populateBulkSubscribeResponsesWithError(msg.entries, bulkResponses, &entryIdIndexMap, nil)
			return nil
		}

		err = errors.Errorf("error returned from app while processing bulk pub/sub event: %s", err)
		log.Debug(err)
		bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += int64(len(msg.entries))
		bulkSubDiag.elapsed = elapsed
		populateBulkSubscribeResponsesWithError(msg.entries, bulkResponses, &entryIdIndexMap, err)
		// on error from application, return error for redelivery of event
		return nil
	}

	hasAnyError := false
	for _, response := range res.GetStatuses() {
		if entryId, ok := entryIdIndexMap[response.EntryId]; ok { //nolint:stylecheck
			switch response.GetStatus() {
			case runtimev1pb.TopicEventResponse_SUCCESS: //nolint:nosnakecase
				// on uninitialized status, this is the case it defaults to as an uninitialized status defaults to 0 which is
				// success from protobuf definition
				bulkSubDiag.statusWiseDiag[string(pubsub.Success)] += 1
				setBulkResponseEntry(bulkResponses, entryId, response.EntryId, nil)
			case runtimev1pb.TopicEventResponse_RETRY: //nolint:nosnakecase
				bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += 1
				setBulkResponseEntry(bulkResponses, entryId, response.EntryId,
					errors.Errorf("RETRY status returned from app while processing pub/sub event for entry id: %v", response.EntryId))
				hasAnyError = true
			case runtimev1pb.TopicEventResponse_DROP: //nolint:nosnakecase
				log.Warnf("DROP status returned from app while processing pub/sub event for entry id: %v", response.EntryId)
				bulkSubDiag.statusWiseDiag[string(pubsub.Drop)] += 1
				setBulkResponseEntry(bulkResponses, entryId, response.EntryId, nil)
			default:
				// Consider unknown status field as error and retry
				bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += 1
				setBulkResponseEntry(bulkResponses, entryId, response.EntryId,
					errors.Errorf("unknown status returned from app while processing pub/sub event  for entry id %v: %v", response.EntryId, response.GetStatus()))
				hasAnyError = true
			}
		} else {
			log.Warnf("Invalid entry id received from app while processing pub/sub event %v", response.EntryId)
			continue
		}
	}
	for _, item := range msg.entries {
		ind := entryIdIndexMap[item.EntryId]
		if (*bulkResponses)[ind].EntryId == "" {
			setBulkResponseEntry(bulkResponses, ind, item.EntryId,
				errors.Errorf("Response not received, RETRY required while processing bulk subscribe event for entry id: %v", item.EntryId))
			hasAnyError = true
			bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += 1
		}
	}
	bulkSubDiag.elapsed = elapsed
	if hasAnyError {
		return errors.New("Few message(s) have failed during bulk subscribe operation")
	} else {
		return nil
	}
}

func endSpans(spans []trace.Span) {
	for _, span := range spans {
		if span != nil {
			span.End()
		}
	}
}

// sendBulkToDeadLetter sends the bulk message to deadletter topic.
func (a *DaprRuntime) sendBulkToDeadLetter(
	ctx context.Context, name string, msg *pubsub.BulkMessage, deadLetterTopic string,
	entryIdIndexMap *map[string]int, bulkResponses *[]pubsub.BulkSubscribeResponseEntry, //nolint:stylecheck
	bulkSubDiag *bulkSubIngressDiagnostics,
) error {
	data := make([]pubsub.BulkMessageEntry, len(msg.Entries))

	if bulkResponses == nil {
		data = msg.Entries
	} else {
		n := 0
		for _, message := range msg.Entries {
			entryId := (*entryIdIndexMap)[message.EntryId] //nolint:stylecheck
			if (*bulkResponses)[entryId].Error != nil {
				data[n] = message
				n++
			}
		}
		data = data[:n]
	}
	bulkSubDiag.statusWiseDiag[string(pubsub.Drop)] += int64(len(data))
	if bulkSubDiag.retryReported {
		bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] -= int64(len(data))
	}
	req := &pubsub.BulkPublishRequest{
		Entries:    data,
		PubsubName: name,
		Topic:      deadLetterTopic,
		Metadata:   msg.Metadata,
	}

	_, err := a.BulkPublish(req)
	if err != nil {
		log.Errorf("error sending message to dead letter, origin topic: %s dead letter topic %s err: %w", msg.Topic, deadLetterTopic, err)
	}
	return err
}

func validateEntryId(entryId string, i int) error { //nolint:stylecheck
	if entryId == "" {
		log.Warn("Invalid blank entry id received while processing bulk pub/sub event, won't be able to process it")
		return errors.New("Blank entryId supplied - won't be able to process it")
	}
	return nil
}

func populateBulkSubcribedMessage(message *pubsub.BulkMessageEntry, event interface{},
	routePathBulkMessageMap *map[string]pubsubBulkSubscribedMessage,
	rPath string, i int, msg *pubsub.BulkMessage, isCloudEvent bool, psName string, contentType string, namespacedConsumer bool, namespace string,
) {
	childMessage := runtimePubsub.BulkSubscribeMessageItem{
		Event:       event,
		Metadata:    message.Metadata,
		EntryId:     message.EntryId,
		ContentType: contentType,
	}
	var cloudEvent map[string]interface{}
	mapTypeEvent, ok := event.(map[string]interface{})
	if ok {
		cloudEvent = mapTypeEvent
	}
	if val, ok := (*routePathBulkMessageMap)[rPath]; ok {
		if isCloudEvent {
			val.cloudEvents[val.length] = mapTypeEvent
		}
		val.rawData[val.length] = childMessage
		val.entries[val.length] = &msg.Entries[i]
		val.length++
		(*routePathBulkMessageMap)[rPath] = val
	} else {
		cloudEvents := make([]map[string]interface{}, len(msg.Entries))
		rawDataItems := make([]runtimePubsub.BulkSubscribeMessageItem, len(msg.Entries))
		rawDataItems[0] = childMessage
		entries := make([]*pubsub.BulkMessageEntry, len(msg.Entries))
		entries[0] = &msg.Entries[i]
		if isCloudEvent {
			cloudEvents[0] = cloudEvent
		}

		msgTopic := msg.Topic
		if namespacedConsumer {
			msgTopic = strings.Replace(msgTopic, namespace, "", 1)
		}

		psm := pubsubBulkSubscribedMessage{
			cloudEvents: cloudEvents,
			rawData:     rawDataItems,
			entries:     entries,
			topic:       msgTopic,
			metadata:    msg.Metadata,
			pubsub:      psName,
			length:      1,
		}
		(*routePathBulkMessageMap)[rPath] = psm
	}
}

func populateBulkSubscribeResponsesWithError(entries []*pubsub.BulkMessageEntry,
	bulkResponses *[]pubsub.BulkSubscribeResponseEntry, entryIdIndexMap *map[string]int, err error, //nolint:stylecheck
) {
	for _, item := range entries {
		ind := (*entryIdIndexMap)[item.EntryId]
		if (*bulkResponses)[ind].EntryId == "" {
			setBulkResponseEntry(bulkResponses, ind, item.EntryId, err)
		}
	}
}

func populateAllBulkResponsesWithError(bulkMsg *pubsub.BulkMessage,
	bulkResponses *[]pubsub.BulkSubscribeResponseEntry, err error,
) {
	for i, item := range bulkMsg.Entries {
		if (*bulkResponses)[i].EntryId == "" {
			setBulkResponseEntry(bulkResponses, i, item.EntryId, err)
		}
	}
}

func setBulkResponseEntry(bulkResponses *[]pubsub.BulkSubscribeResponseEntry, i int, entryId string, err error) { //nolint:stylecheck
	(*bulkResponses)[i].EntryId = entryId
	(*bulkResponses)[i].Error = err
}

func newBulkSubIngressDiagnostics() bulkSubIngressDiagnostics {
	statusWiseCountDiag := make(map[string]int64, 3)
	statusWiseCountDiag[string(pubsub.Success)] = 0
	statusWiseCountDiag[string(pubsub.Drop)] = 0
	statusWiseCountDiag[string(pubsub.Retry)] = 0
	bulkSubDiag := bulkSubIngressDiagnostics{
		statusWiseDiag: statusWiseCountDiag,
		elapsed:        0,
		retryReported:  false,
	}
	return bulkSubDiag
}

func reportBulkSubDiagnostics(ctx context.Context, topic string, bulkSubDiag *bulkSubIngressDiagnostics) {
	if bulkSubDiag == nil {
		return
	}
	diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, topic, bulkSubDiag.elapsed)
	for status, count := range bulkSubDiag.statusWiseDiag {
		diag.DefaultComponentMonitoring.BulkPubsubIngressEventEntries(ctx, pubsubName, topic, status, count)
	}
}
