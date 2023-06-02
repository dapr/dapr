package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/google/uuid"
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
	"github.com/dapr/dapr/pkg/runtime/compstore"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

// pubSubMessage contains all the essential information related to a particular entry.
// This need to be maintained as a separate struct, as we need to filter out messages and
// their related info doing retries of resiliency support.
type pubSubMessage struct {
	cloudEvent map[string]interface{}
	rawData    *runtimePubsub.BulkSubscribeMessageItem
	entry      *pubsub.BulkMessageEntry
}

// pubsubBulkSubscribedMessage contains all the essential information related to
// a bulk subscribe message.
type pubsubBulkSubscribedMessage struct {
	pubSubMessages []pubSubMessage
	topic          string
	metadata       map[string]string
	pubsub         string
	path           string
	length         int
}

// bulkSubIngressDiagnostics holds diagnostics information for bulk subscribe ingress.
type bulkSubIngressDiagnostics struct {
	statusWiseDiag map[string]int64
	elapsed        float64
	retryReported  bool
}

// bulkSubscribeCallData holds data for a bulk subscribe call.
type bulkSubscribeCallData struct {
	bulkResponses   *[]pubsub.BulkSubscribeResponseEntry
	bulkSubDiag     *bulkSubIngressDiagnostics
	entryIdIndexMap *map[string]int //nolint:stylecheck
	psName          string
	topic           string
}

// bulkSubscribeTopic subscribes to a topic for bulk messages and invokes subscriber app endpoint(s).

// Major steps inside a bulk handler:
//  1. Deserialize pubsub metadata and determine if rawPayload or not
//     1.A. If any error occurs, send to DLQ if configured, else send back error for all messages
//  2. Iterate through each message and validate entryId is NOT blank
//     2.A. If it is a raw payload:
//     2.A.i. Get route path, if processable
//     2.A.ii. Check route path is non-blank if protocol used is HTTP; generate base64 encoding of event data
//     and set contentType, if provided, else set to "application/octet-stream"
//     2.A.iii. Finally, form a child message to be sent to app and add it to the list of messages,
//     to be sent to app (this list of messages is registered against correct path in an internal map)
//     2.B. If it is NOT a raw payload (it is considered a cloud event):
//     2.B.i. Unmarshal it into a map[string]interface{}
//     2.B.ii. If any error while unmarshalling, register error for this message
//     2.B.iii. Check if message expired
//     2.B.iv. Get route path, if processable
//     2.B.v. Check route path is non-blank if protocol used is HTTP, form a child message to be sent to app and add it to the list of messages,
//  3. Iterate through map prepared for path vs list of messages to be sent on this path
//     3.A. Prepare envelope for the list of messages to be sent to app on this path
//     3.B. Send the envelope to app by invoking http/grpc endpoint
//  4. Check if any error has occurred so far in processing for any of the message and invoke DLQ, if configured.
//  5. Send back responses array to broker interface.
func (a *DaprRuntime) bulkSubscribeTopic(ctx context.Context, policyDef *resiliency.PolicyDefinition,
	psName string, topic string, route compstore.TopicRouteElem, namespacedConsumer bool,
) error {
	ps, ok := a.compStore.GetPubSub(psName)
	if !ok {
		return runtimePubsub.NotFoundError{PubsubName: psName}
	}

	subscribeTopic := topic
	if namespacedConsumer {
		subscribeTopic = a.namespace + topic
	}

	req := pubsub.SubscribeRequest{
		Topic:    subscribeTopic,
		Metadata: route.Metadata,
		BulkSubscribeConfig: pubsub.BulkSubscribeConfig{
			MaxMessagesCount:   int(route.BulkSubscribe.MaxMessagesCount),
			MaxAwaitDurationMs: int(route.BulkSubscribe.MaxAwaitDurationMs),
		},
	}

	bulkHandler := func(ctx context.Context, msg *pubsub.BulkMessage) ([]pubsub.BulkSubscribeResponseEntry, error) {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]string, 1)
		}

		msg.Metadata[pubsubName] = psName
		bulkSubDiag := newBulkSubIngressDiagnostics()
		bulkResponses := make([]pubsub.BulkSubscribeResponseEntry, len(msg.Entries))
		routePathBulkMessageMap := make(map[string]pubsubBulkSubscribedMessage)
		entryIdIndexMap := make(map[string]int, len(msg.Entries)) //nolint:stylecheck
		bulkSubCallData := bulkSubscribeCallData{
			bulkResponses:   &bulkResponses,
			bulkSubDiag:     &bulkSubDiag,
			entryIdIndexMap: &entryIdIndexMap,
			psName:          psName,
			topic:           topic,
		}
		rawPayload, err := contribMetadata.IsRawPayload(route.Metadata)
		if err != nil {
			log.Errorf("error deserializing pubsub metadata: %s", err)
			if dlqErr := a.sendBulkToDLQIfConfigured(ctx, &bulkSubCallData, msg, true, route); dlqErr != nil {
				populateAllBulkResponsesWithError(msg, &bulkResponses, err)
				reportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
				return bulkResponses, err
			}
			reportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
			return nil, nil
		}
		hasAnyError := false
		for i, message := range msg.Entries {
			if entryIdErr := validateEntryId(message.EntryId, i); entryIdErr != nil { //nolint:stylecheck
				bulkResponses[i].Error = entryIdErr
				hasAnyError = true
				continue
			}
			entryIdIndexMap[message.EntryId] = i
			if rawPayload {
				rPath, routeErr := a.getRouteIfProcessable(ctx, &bulkSubCallData, route, &(msg.Entries[i]), i, string(message.Event))
				if routeErr != nil {
					hasAnyError = true
					continue
				}
				// For grpc, we can still send the entry even if path is blank, App can take a decision
				if rPath == "" && a.runtimeConfig.appConnectionConfig.Protocol.IsHTTP() {
					continue
				}
				dataB64 := base64.StdEncoding.EncodeToString(message.Event)
				if message.ContentType == "" {
					message.ContentType = "application/octet-stream"
				}
				populateBulkSubcribedMessage(&(msg.Entries[i]), dataB64, &routePathBulkMessageMap, rPath, i, msg, false, psName, message.ContentType, namespacedConsumer, a.namespace)
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
					if route.DeadLetterTopic != "" {
						_ = a.sendToDeadLetter(psName, &pubsub.NewMessage{
							Data:        message.Event,
							Topic:       topic,
							Metadata:    message.Metadata,
							ContentType: &message.ContentType,
						}, route.DeadLetterTopic)
					}
					bulkResponses[i].EntryId = message.EntryId
					bulkResponses[i].Error = nil
					continue
				}
				rPath, routeErr := a.getRouteIfProcessable(ctx, &bulkSubCallData, route, &(msg.Entries[i]), i, cloudEvent)
				if routeErr != nil {
					hasAnyError = true
					continue
				}
				// For grpc, we can still send the entry even if path is blank, App can take a decision
				if rPath == "" && a.runtimeConfig.appConnectionConfig.Protocol.IsHTTP() {
					continue
				}
				if message.ContentType == "" {
					message.ContentType = contenttype.CloudEventContentType
				}
				populateBulkSubcribedMessage(&(msg.Entries[i]), cloudEvent, &routePathBulkMessageMap, rPath, i, msg, true, psName, message.ContentType, namespacedConsumer, a.namespace)
			}
		}
		var overallInvokeErr error
		for path, psm := range routePathBulkMessageMap {
			invokeErr := a.createEnvelopeAndInvokeSubscriber(ctx, &bulkSubCallData, psm, msg, route, path, policyDef, rawPayload)
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
			if dlqErr := a.sendBulkToDLQIfConfigured(ctx, &bulkSubCallData, msg, false, route); dlqErr != nil {
				reportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
				return bulkResponses, err
			}
			reportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
			return nil, nil
		}
		reportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
		return bulkResponses, err
	}

	if bulkSubscriber, ok := ps.Component.(pubsub.BulkSubscriber); ok {
		return bulkSubscriber.BulkSubscribe(ctx, req, bulkHandler)
	}

	return runtimePubsub.NewDefaultBulkSubscriber(ps.Component).BulkSubscribe(ctx, req, bulkHandler)
}

// sendBulkToDLQIfConfigured sends the message to the dead letter queue if configured.
func (a *DaprRuntime) sendBulkToDLQIfConfigured(ctx context.Context, bulkSubCallData *bulkSubscribeCallData, msg *pubsub.BulkMessage,
	sendAllEntries bool, route compstore.TopicRouteElem,
) error {
	bscData := *bulkSubCallData
	if route.DeadLetterTopic != "" {
		if dlqErr := a.sendBulkToDeadLetter(bulkSubCallData, msg, route.DeadLetterTopic, sendAllEntries); dlqErr == nil {
			// dlq has been configured and whole bulk of messages is successfully sent to dlq.
			return nil
		}
	}
	if !bscData.bulkSubDiag.retryReported {
		bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += int64(len(msg.Entries))
	}
	return errors.New("failed to send to DLQ as DLQ was not configured")
}

// getRouteIfProcessable returns the route path if the message is processable.
func (a *DaprRuntime) getRouteIfProcessable(ctx context.Context, bulkSubCallData *bulkSubscribeCallData, route compstore.TopicRouteElem, message *pubsub.BulkMessageEntry,
	i int, matchElem interface{},
) (string, error) {
	bscData := *bulkSubCallData
	rPath, shouldProcess, routeErr := findMatchingRoute(route.Rules, matchElem)
	if routeErr != nil {
		log.Errorf("Error finding matching route for event in bulk subscribe %s and topic %s for entry id %s: %s", bscData.psName, bscData.topic, message.EntryId, routeErr)
		setBulkResponseEntry(bscData.bulkResponses, i, message.EntryId, routeErr)
		return "", routeErr
	}
	if !shouldProcess {
		// The event does not match any route specified so ignore it.
		log.Warnf("No matching route for event in pubsub %s and topic %s; skipping", bscData.psName, bscData.topic)
		bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Drop)]++
		if route.DeadLetterTopic != "" {
			_ = a.sendToDeadLetter(bscData.psName, &pubsub.NewMessage{
				Data:        message.Event,
				Topic:       bscData.topic,
				Metadata:    message.Metadata,
				ContentType: &message.ContentType,
			}, route.DeadLetterTopic)
		}
		setBulkResponseEntry(bscData.bulkResponses, i, message.EntryId, nil)
		return "", nil
	}
	return rPath, nil
}

// createEnvelopeAndInvokeSubscriber creates the envelope and invokes the subscriber.
func (a *DaprRuntime) createEnvelopeAndInvokeSubscriber(ctx context.Context, bulkSubCallData *bulkSubscribeCallData, psm pubsubBulkSubscribedMessage,
	msg *pubsub.BulkMessage, route compstore.TopicRouteElem, path string, policyDef *resiliency.PolicyDefinition,
	rawPayload bool,
) error {
	bscData := *bulkSubCallData
	var id string
	idObj, err := uuid.NewRandom()
	if err != nil {
		id = idObj.String()
	}
	psm.pubSubMessages = psm.pubSubMessages[:psm.length]
	psm.path = path
	envelope := runtimePubsub.NewBulkSubscribeEnvelope(&runtimePubsub.BulkSubscribeEnvelope{
		ID:       id,
		Topic:    bscData.topic,
		Pubsub:   bscData.psName,
		Metadata: msg.Metadata,
	})
	_, e := a.ApplyBulkSubscribeResiliency(ctx, bulkSubCallData, psm, route.DeadLetterTopic,
		path, policyDef, rawPayload, envelope)
	return e
}

// publishBulkMessageHTTP publishes bulk message to a subscriber using HTTP and takes care of corresponding responses.
func (a *DaprRuntime) publishBulkMessageHTTP(ctx context.Context, bulkSubCallData *bulkSubscribeCallData, psm *pubsubBulkSubscribedMessage,
	bulkResponses *[]pubsub.BulkSubscribeResponseEntry, envelope map[string]any, deadLetterTopic string,
) error {
	bscData := *bulkSubCallData
	rawMsgEntries := make([]*runtimePubsub.BulkSubscribeMessageItem, len(psm.pubSubMessages))
	entryRespReceived := make(map[string]bool, len(psm.pubSubMessages))
	for i, pubSubMsg := range psm.pubSubMessages {
		rawMsgEntries[i] = pubSubMsg.rawData
	}

	a.bulkSubLock.Lock()
	envelope[runtimePubsub.Entries] = rawMsgEntries
	da, marshalErr := json.Marshal(&envelope)
	a.bulkSubLock.Unlock()

	if marshalErr != nil {
		log.Errorf("Error serializing bulk cloud event in pubsub %s and topic %s: %s", psm.pubsub, psm.topic, marshalErr)
		if deadLetterTopic != "" {
			entries := make([]pubsub.BulkMessageEntry, len(psm.pubSubMessages))
			for i, pubsubMsg := range psm.pubSubMessages {
				entries[i] = *pubsubMsg.entry
			}
			bulkMsg := pubsub.BulkMessage{
				Entries:  entries,
				Topic:    psm.topic,
				Metadata: psm.metadata,
			}
			if dlqErr := a.sendBulkToDeadLetter(bulkSubCallData, &bulkMsg, deadLetterTopic, true); dlqErr == nil {
				// dlq has been configured and message is successfully sent to dlq.
				for _, item := range rawMsgEntries {
					addBulkResponseEntry(bulkResponses, item.EntryId, nil)
				}
				return nil
			}
		}
		bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += int64(len(rawMsgEntries))

		for _, item := range rawMsgEntries {
			addBulkResponseEntry(bulkResponses, item.EntryId, marshalErr)
		}
		return marshalErr
	}

	spans := make([]trace.Span, len(rawMsgEntries))

	req := invokev1.NewInvokeMethodRequest(psm.path).
		WithHTTPExtension(nethttp.MethodPost, "").
		WithRawDataBytes(da).
		WithContentType(contenttype.JSONContentType).
		WithCustomHTTPMetadata(psm.metadata)
	defer req.Close()

	n := 0
	for _, pubsubMsg := range psm.pubSubMessages {
		cloudEvent := pubsubMsg.cloudEvent
		iTraceID := cloudEvent[pubsub.TraceParentField]
		if iTraceID == nil {
			iTraceID = cloudEvent[pubsub.TraceIDField]
		}
		if iTraceID != nil {
			traceID := iTraceID.(string)
			sc, _ := diag.SpanContextFromW3CString(traceID)
			var span trace.Span
			ctx, span = diag.StartInternalCallbackSpan(ctx, "pubsub/"+psm.topic, sc, a.globalConfig.Spec.TracingSpec)
			if span != nil {
				spans[n] = span
				n++
			}
		}
	}
	spans = spans[:n]
	defer endSpans(spans)
	start := time.Now()
	resp, err := a.appChannel.InvokeMethod(ctx, req, "")
	elapsed := diag.ElapsedSince(start)
	if err != nil {
		bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += int64(len(rawMsgEntries))
		bscData.bulkSubDiag.elapsed = elapsed
		populateBulkSubscribeResponsesWithError(psm, bulkResponses, err)
		return fmt.Errorf("error from app channel while sending pub/sub event to app: %w", err)
	}
	defer resp.Close()

	statusCode := int(resp.Status().Code)

	for _, span := range spans {
		m := diag.ConstructSubscriptionSpanAttributes(psm.topic)
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromHTTPStatus(span, statusCode)
	}

	if (statusCode >= 200) && (statusCode <= 299) {
		// Any 2xx is considered a success.
		var appBulkResponse pubsub.AppBulkResponse
		err := json.NewDecoder(resp.RawData()).Decode(&appBulkResponse)
		if err != nil {
			bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += int64(len(rawMsgEntries))
			bscData.bulkSubDiag.elapsed = elapsed
			populateBulkSubscribeResponsesWithError(psm, bulkResponses, err)
			return fmt.Errorf("failed unmarshalling app response for bulk subscribe: %w", err)
		}

		var hasAnyError bool
		for _, response := range appBulkResponse.AppResponses {
			if _, ok := (*bscData.entryIdIndexMap)[response.EntryId]; ok {
				switch response.Status {
				case "":
					// When statusCode 2xx, Consider empty status field OR not receiving status for an item as retry
					fallthrough
				case pubsub.Retry:
					a.bulkSubLock.Lock()
					bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)]++
					a.bulkSubLock.Unlock()
					entryRespReceived[response.EntryId] = true
					addBulkResponseEntry(bulkResponses, response.EntryId,
						fmt.Errorf("RETRY required while processing bulk subscribe event for entry id: %v", response.EntryId))
					hasAnyError = true
				case pubsub.Success:
					a.bulkSubLock.Lock()
					bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Success)]++
					a.bulkSubLock.Unlock()
					entryRespReceived[response.EntryId] = true
					addBulkResponseEntry(bulkResponses, response.EntryId, nil)
				case pubsub.Drop:
					a.bulkSubLock.Lock()
					bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Drop)]++
					a.bulkSubLock.Unlock()
					entryRespReceived[response.EntryId] = true
					log.Warnf("DROP status returned from app while processing pub/sub event %v", response.EntryId)
					addBulkResponseEntry(bulkResponses, response.EntryId, nil)
				default:
					// Consider unknown status field as error and retry
					a.bulkSubLock.Lock()
					bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)]++
					a.bulkSubLock.Unlock()
					entryRespReceived[response.EntryId] = true
					addBulkResponseEntry(bulkResponses, response.EntryId,
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
				addBulkResponseEntry(bulkResponses, item.EntryId,
					fmt.Errorf("Response not received, RETRY required while processing bulk subscribe event for entry id: %v", item.EntryId), //nolint:stylecheck
				)
				hasAnyError = true
				bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)]++
			}
		}
		bscData.bulkSubDiag.elapsed = elapsed
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
		bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Drop)] += int64(len(rawMsgEntries))
		bscData.bulkSubDiag.elapsed = elapsed
		populateBulkSubscribeResponsesWithError(psm, bulkResponses, nil)
		return nil
	}

	// Every error from now on is a retriable error.
	retriableErrorStr := fmt.Sprintf("Retriable error returned from app while processing bulk pub/sub event, topic: %v. status code returned: %v", psm.topic, statusCode)
	retriableError := errors.New(retriableErrorStr)
	log.Warn(retriableErrorStr)
	bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += int64(len(rawMsgEntries))
	bscData.bulkSubDiag.elapsed = elapsed
	populateBulkSubscribeResponsesWithError(psm, bulkResponses, retriableError)
	return retriableError
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
func (a *DaprRuntime) publishBulkMessageGRPC(ctx context.Context, bulkSubCallData *bulkSubscribeCallData, psm *pubsubBulkSubscribedMessage,
	bulkResponses *[]pubsub.BulkSubscribeResponseEntry, rawPayload bool,
) error {
	bscData := *bulkSubCallData
	items := make([]*runtimev1pb.TopicEventBulkRequestEntry, len(psm.pubSubMessages))
	entryRespReceived := make(map[string]bool, len(psm.pubSubMessages))
	for i, pubSubMsg := range psm.pubSubMessages {
		entry := pubSubMsg.entry
		item, err := fetchEntry(rawPayload, entry, psm.pubSubMessages[i].cloudEvent)
		if err != nil {
			bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)]++
			addBulkResponseEntry(bulkResponses, entry.EntryId, err)
			continue
		}
		items[i] = item
	}

	uuidObj, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("failed to generate UUID: %w", err)
	}
	envelope := &runtimev1pb.TopicEventBulkRequest{
		Id:         uuidObj.String(),
		Entries:    items,
		Metadata:   psm.metadata,
		Topic:      psm.topic,
		PubsubName: psm.pubsub,
		Type:       pubsub.DefaultBulkEventType,
		Path:       psm.path,
	}

	spans := make([]trace.Span, len(psm.pubSubMessages))
	n := 0
	for _, pubSubMsg := range psm.pubSubMessages {
		cloudEvent := pubSubMsg.cloudEvent
		iTraceID := cloudEvent[pubsub.TraceParentField]
		if iTraceID == nil {
			iTraceID = cloudEvent[pubsub.TraceIDField]
		}
		if iTraceID != nil {
			if traceID, ok := iTraceID.(string); ok {
				sc, _ := diag.SpanContextFromW3CString(traceID)

				// no ops if trace is off
				var span trace.Span
				ctx, span = diag.StartInternalCallbackSpan(ctx, "pubsub/"+psm.topic, sc, a.globalConfig.Spec.TracingSpec)
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
	defer endSpans(spans)
	ctx = invokev1.WithCustomGRPCMetadata(ctx, psm.metadata)

	conn, err := a.grpc.GetAppClient()
	defer a.grpc.ReleaseAppClient(conn)
	if err != nil {
		return fmt.Errorf("error while getting app client: %w", err)
	}
	clientV1 := runtimev1pb.NewAppCallbackAlphaClient(conn)

	start := time.Now()
	res, err := clientV1.OnBulkTopicEventAlpha1(ctx, envelope)
	elapsed := diag.ElapsedSince(start)

	for _, span := range spans {
		m := diag.ConstructSubscriptionSpanAttributes(envelope.Topic)
		diag.AddAttributesToSpan(span, m)
		diag.UpdateSpanStatusFromGRPCError(span, err)
	}

	if err != nil {
		errStatus, hasErrStatus := status.FromError(err)
		if hasErrStatus && (errStatus.Code() == codes.Unimplemented) {
			// DROP
			log.Warnf("non-retriable error returned from app while processing bulk pub/sub event: %s", err)
			bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Drop)] += int64(len(psm.pubSubMessages))
			bscData.bulkSubDiag.elapsed = elapsed
			populateBulkSubscribeResponsesWithError(psm, bulkResponses, nil)
			return nil
		}

		err = fmt.Errorf("error returned from app while processing bulk pub/sub event: %w", err)
		log.Debug(err)
		bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += int64(len(psm.pubSubMessages))
		bscData.bulkSubDiag.elapsed = elapsed
		populateBulkSubscribeResponsesWithError(psm, bulkResponses, err)
		// on error from application, return error for redelivery of event
		return err
	}

	hasAnyError := false
	for _, response := range res.GetStatuses() {
		if _, ok := (*bscData.entryIdIndexMap)[response.EntryId]; ok {
			switch response.GetStatus() {
			case runtimev1pb.TopicEventResponse_SUCCESS: //nolint:nosnakecase
				// on uninitialized status, this is the case it defaults to as an uninitialized status defaults to 0 which is
				// success from protobuf definition
				a.bulkSubLock.Lock()
				bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Success)] += 1
				a.bulkSubLock.Unlock()
				entryRespReceived[response.EntryId] = true
				addBulkResponseEntry(bulkResponses, response.EntryId, nil)
			case runtimev1pb.TopicEventResponse_RETRY: //nolint:nosnakecase
				a.bulkSubLock.Lock()
				bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += 1
				a.bulkSubLock.Unlock()
				entryRespReceived[response.EntryId] = true
				addBulkResponseEntry(bulkResponses, response.EntryId,
					fmt.Errorf("RETRY status returned from app while processing pub/sub event for entry id: %v", response.EntryId))
				hasAnyError = true
			case runtimev1pb.TopicEventResponse_DROP: //nolint:nosnakecase
				log.Warnf("DROP status returned from app while processing pub/sub event for entry id: %v", response.EntryId)
				a.bulkSubLock.Lock()
				bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Drop)] += 1
				a.bulkSubLock.Unlock()
				entryRespReceived[response.EntryId] = true
				addBulkResponseEntry(bulkResponses, response.EntryId, nil)
			default:
				// Consider unknown status field as error and retry
				a.bulkSubLock.Lock()
				bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += 1
				a.bulkSubLock.Unlock()
				entryRespReceived[response.EntryId] = true
				addBulkResponseEntry(bulkResponses, response.EntryId,
					fmt.Errorf("unknown status returned from app while processing pub/sub event  for entry id %v: %v", response.EntryId, response.GetStatus()))
				hasAnyError = true
			}
		} else {
			log.Warnf("Invalid entry id received from app while processing pub/sub event %v", response.EntryId)
			continue
		}
	}
	for _, item := range psm.pubSubMessages {
		if !entryRespReceived[item.entry.EntryId] {
			addBulkResponseEntry(bulkResponses, item.entry.EntryId,
				fmt.Errorf("Response not received, RETRY required while processing bulk subscribe event for entry id: %v", item.entry.EntryId), //nolint:stylecheck
			)
			hasAnyError = true
			bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] += 1
		}
	}
	bscData.bulkSubDiag.elapsed = elapsed
	if hasAnyError {
		//nolint:stylecheck
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
	bulkSubCallData *bulkSubscribeCallData, msg *pubsub.BulkMessage, deadLetterTopic string,
	sendAllEntries bool,
) error {
	bscData := *bulkSubCallData
	data := make([]pubsub.BulkMessageEntry, len(msg.Entries))

	if sendAllEntries {
		data = msg.Entries
	} else {
		n := 0
		for _, message := range msg.Entries {
			entryId := (*bscData.entryIdIndexMap)[message.EntryId] //nolint:stylecheck
			if (*bscData.bulkResponses)[entryId].Error != nil {
				data[n] = message
				n++
			}
		}
		data = data[:n]
	}
	bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Drop)] += int64(len(data))
	if bscData.bulkSubDiag.retryReported {
		bscData.bulkSubDiag.statusWiseDiag[string(pubsub.Retry)] -= int64(len(data))
	}
	req := &pubsub.BulkPublishRequest{
		Entries:    data,
		PubsubName: bscData.psName,
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
		//nolint:stylecheck
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
			val.pubSubMessages[val.length].cloudEvent = mapTypeEvent
		}
		val.pubSubMessages[val.length].rawData = &childMessage
		val.pubSubMessages[val.length].entry = &msg.Entries[i]
		val.length++
		(*routePathBulkMessageMap)[rPath] = val
	} else {
		pubSubMessages := make([]pubSubMessage, len(msg.Entries))
		pubSubMessages[0].rawData = &childMessage
		pubSubMessages[0].entry = &msg.Entries[i]
		if isCloudEvent {
			pubSubMessages[0].cloudEvent = cloudEvent
		}

		msgTopic := msg.Topic
		if namespacedConsumer {
			msgTopic = strings.Replace(msgTopic, namespace, "", 1)
		}

		psm := pubsubBulkSubscribedMessage{
			pubSubMessages: pubSubMessages,
			topic:          msgTopic,
			metadata:       msg.Metadata,
			pubsub:         psName,
			length:         1,
		}
		(*routePathBulkMessageMap)[rPath] = psm
	}
}

func populateBulkSubscribeResponsesWithError(psm *pubsubBulkSubscribedMessage,
	bulkResponses *[]pubsub.BulkSubscribeResponseEntry, err error,
) {
	for _, message := range psm.pubSubMessages {
		addBulkResponseEntry(bulkResponses, message.entry.EntryId, err)
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

func addBulkResponseEntry(bulkResponses *[]pubsub.BulkSubscribeResponseEntry, entryId string, err error) { //nolint:stylecheck
	resp := pubsub.BulkSubscribeResponseEntry{
		EntryId: entryId,
		Error:   err,
	}
	*bulkResponses = append(*bulkResponses, resp)
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
