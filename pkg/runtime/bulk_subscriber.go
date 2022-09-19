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

	"github.com/dapr/components-contrib/contenttype"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
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

// bulkSubscribeTopic subscribes to a topic for bulk messages and invokes subscriber app endpoint(s).

// Major steps inside a bulk handler:
//  1. Deserialize pubsub metadata and determine if rawPayload or not
//     1A. If any error occurs, send to DLQ if configured, else send back error for all messages
//  2. Iterate through each message and validate entryID is NOT blank
//     2A. If it is a raw payload:
//     2Aa. Get route path, if processable
//     2Ab. If route path is non-blank, generate base64 encoding of event data
//     and set contentType, if provided, else set to "application/octet-stream"
//     2Ac. Finally, form a child message to be sent to app and add it to the list of messages,
//     to be sent to app (this list of messages is registered against correct path in an internal map)
//     2B. If it is NOT a raw payload (it is considered a cloud event):
//     2Ba. Unmarshal it into a map[string]interface{}
//     2Bb. If any error while unmatrshalling, send to DLQ if configured, else register error for this message
//     2Bc. Check if message expired
//     2Bd. Get route path, if processable
//     2Bb. If route path is non-blank, form a child message to be sent to app and add it to the list of messages,
//  3. Iterate through map prepared for path vs list of messages to be sent on this path
//     3A. Prepare envelope for the list of messages to be sent to app on this path
//     3B. Send the envelope to app by invoking http endpoint
//  4. Check if any error has occurred so far in processing for any of the message and invoke DLQ, if configured.
//  5. Send back responses array to broker interface.
func (a *DaprRuntime) bulkSubscribeTopic(ctx context.Context, policy resiliency.Runner,
	psName string, topic string, route TopicRouteElem,
) error {
	ps, ok := a.pubSubs[psName]
	if !ok {
		return runtimePubsub.NotFoundError{PubsubName: psName}
	}

	req := pubsub.SubscribeRequest{
		Topic:    topic,
		Metadata: route.metadata,
	}

	bulkHandler := func(ctx context.Context, msg *pubsub.BulkMessage) ([]pubsub.BulkSubscribeResponseEntry, error) {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]string, 1)
		}

		msg.Metadata[pubsubName] = psName

		rawPayload, err := contribMetadata.IsRawPayload(route.metadata)
		if err != nil {
			log.Errorf("error deserializing pubsub metadata: %s", err)
			if dlqErr := a.sendBulkToDLQIfConfigured(ctx, psName, msg, route, nil, nil); dlqErr != nil {
				return nil, err
			}
			return nil, nil
		}
		routePathBulkMessageMap := make(map[string]pubsubBulkSubscribedMessage)
		bulkResponses := make([]pubsub.BulkSubscribeResponseEntry, len(msg.Entries))
		entryIDIndexMap := make(map[string]int)
		hasAnyError := false
		for i, message := range msg.Entries {
			if entryIDErr := validateEntryID(message.EntryID, i); entryIDErr != nil {
				bulkResponses[i].Error = entryIDErr
				hasAnyError = true
				continue
			}
			entryIDIndexMap[message.EntryID] = i
			if rawPayload {
				rPath, routeErr := a.getRouteIfProcessable(ctx, route, &(msg.Entries[i]), i, &bulkResponses, string(message.Event), psName, topic)
				if routeErr != nil {
					hasAnyError = true
					continue
				}
				if rPath == "" {
					continue
				}
				dataB64 := base64.StdEncoding.EncodeToString(message.Event)
				var contenttype string
				if message.ContentType != "" {
					contenttype = message.ContentType
				} else {
					contenttype = "application/octet-stream"
				}
				populateBulkSubcribedMessage(&(msg.Entries[i]), dataB64, &routePathBulkMessageMap, rPath, i, msg, false, psName, contenttype)
			} else {
				var cloudEvent map[string]interface{}
				err = json.Unmarshal(message.Event, &cloudEvent)
				if err != nil {
					log.Errorf("error deserializing one of the messages in bulk cloud event in pubsub %s and topic %s: %s", psName, msg.Topic, err)
					bulkResponses[i].Error = err
					bulkResponses[i].EntryID = message.EntryID
					hasAnyError = true
					continue
				}
				if pubsub.HasExpired(cloudEvent) {
					log.Warnf("dropping expired pub/sub event %v as of %v", cloudEvent[pubsub.IDField], cloudEvent[pubsub.ExpirationField])
					diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), topic, 0)
					if route.deadLetterTopic != "" {
						_ = a.sendToDeadLetter(psName, &pubsub.NewMessage{
							Data:        message.Event,
							Topic:       topic,
							Metadata:    message.Metadata,
							ContentType: &message.ContentType,
						}, route.deadLetterTopic)
					}
					bulkResponses[i].EntryID = message.EntryID
					bulkResponses[i].Error = nil
					continue
				}
				rPath, routeErr := a.getRouteIfProcessable(ctx, route, &(msg.Entries[i]), i, &bulkResponses, cloudEvent, psName, topic)
				if routeErr != nil {
					hasAnyError = true
					continue
				}
				if rPath == "" {
					continue
				}
				populateBulkSubcribedMessage(&(msg.Entries[i]), cloudEvent, &routePathBulkMessageMap, rPath, i, msg, true, psName, message.ContentType)
			}
		}
		for path, psm := range routePathBulkMessageMap {
			invokeErr := a.createEnvelopeAndInvokeSubscriber(ctx, psm, topic, psName, msg, route, &bulkResponses, &entryIDIndexMap, path, policy)
			if invokeErr != nil {
				hasAnyError = true
				err = invokeErr
			}
		}
		if hasAnyError && err != context.Canceled {
			// Sending msg to dead letter queue.
			// If no DLQ is configured, return error for backwards compatibility (component-level retry).
			if dlqErr := a.sendBulkToDLQIfConfigured(ctx, psName, msg, route, &entryIDIndexMap, &bulkResponses); dlqErr != nil {
				return bulkResponses, err
			}
			return nil, nil
		}
		return bulkResponses, err
	}

	if bulkSubscriber, ok := ps.component.(pubsub.BulkSubscriber); ok {
		return bulkSubscriber.BulkSubscribe(ctx, req, bulkHandler)
	}

	return runtimePubsub.NewDefaultBulkSubscriber(ps.component).BulkSubscribe(ctx, req, bulkHandler)
}

// sendBulkToDLQIfConfigured sends the message to the dead letter queue if configured.
func (a *DaprRuntime) sendBulkToDLQIfConfigured(ctx context.Context, psName string, msg *pubsub.BulkMessage, route TopicRouteElem,
	entryIDIndexMap *map[string]int, bulkResponses *[]pubsub.BulkSubscribeResponseEntry,
) error {
	if route.deadLetterTopic != "" {
		if dlqErr := a.sendBulkToDeadLetter(ctx, psName, msg, route.deadLetterTopic, nil, nil); dlqErr == nil {
			// dlq has been configured and whole bulk of messages is successfully sent to dlq.
			diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msg.Topic, 0)
			return nil
		}
	}
	diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msg.Topic, 0)
	return errors.New("failed to send to DLQ as DLQ was not configured")
}

// getRouteIfProcessable returns the route path if the message is processable.
func (a *DaprRuntime) getRouteIfProcessable(ctx context.Context, route TopicRouteElem, message *pubsub.BulkMessageEntry,
	i int, bulkResponses *[]pubsub.BulkSubscribeResponseEntry, matchElem interface{},
	psName string, topic string,
) (string, error) {
	rPath, shouldProcess, routeErr := findMatchingRoute(route.rules, matchElem)
	if routeErr != nil {
		log.Errorf("error finding matching route for event in bulk subscribe %s and topic %s for entry id %s: %s", psName, topic, message.EntryID, routeErr)
		(*bulkResponses)[i].EntryID = message.EntryID
		(*bulkResponses)[i].Error = routeErr
		return "", routeErr
	}
	if !shouldProcess {
		// The event does not match any route specified so ignore it.
		log.Debugf("no matching route for event in pubsub %s and topic %s; skipping", psName, topic)
		diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), topic, 0)
		if route.deadLetterTopic != "" {
			_ = a.sendToDeadLetter(psName, &pubsub.NewMessage{
				Data:        message.Event,
				Topic:       topic,
				Metadata:    message.Metadata,
				ContentType: &message.ContentType,
			}, route.deadLetterTopic)
		}
		(*bulkResponses)[i].EntryID = message.EntryID
		(*bulkResponses)[i].Error = nil
		return "", nil
	}
	return rPath, nil
}

// createEnvelopeAndInvokeSubscriber creates the envelope and invokes the subscriber.
func (a *DaprRuntime) createEnvelopeAndInvokeSubscriber(ctx context.Context, psm pubsubBulkSubscribedMessage, topic string, psName string,
	msg *pubsub.BulkMessage, route TopicRouteElem, bulkResponses *[]pubsub.BulkSubscribeResponseEntry,
	entryIDIndexMap *map[string]int, path string, policy resiliency.Runner,
) error {
	id, _ := uuid.NewRandom()
	psm.cloudEvents = psm.cloudEvents[:psm.length]
	psm.rawData = psm.rawData[:psm.length]
	psm.entries = psm.entries[:psm.length]
	envelope := runtimePubsub.NewBulkSubscribeEnvelope(&runtimePubsub.BulkSubscribeEnvelope{
		ID:       id.String(),
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
			if dlqErr := a.sendBulkToDeadLetter(ctx, psName, &bulkMsg, route.deadLetterTopic, entryIDIndexMap, nil); dlqErr == nil {
				// dlq has been configured and message is successfully sent to dlq.
				diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msg.Topic, 0)
				for _, item := range psm.entries {
					ind := (*entryIDIndexMap)[item.EntryID]
					(*bulkResponses)[ind].EntryID = item.EntryID
					(*bulkResponses)[ind].Error = nil
				}
				return nil
			}
		}
		diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msg.Topic, 0)
		for _, item := range psm.entries {
			ind := (*entryIDIndexMap)[item.EntryID]
			(*bulkResponses)[ind].EntryID = item.EntryID
			(*bulkResponses)[ind].Error = marshalErr
		}
		return marshalErr
	}
	psm.data = da
	psm.path = path
	err := policy(func(ctx context.Context) error {
		switch a.runtimeConfig.ApplicationProtocol {
		case HTTPProtocol:
			psm := psm
			errPub := a.publishBulkMessageHTTP(ctx, &psm, bulkResponses, *entryIDIndexMap)
			return errPub
		default:
			return backoff.Permanent(errors.New("invalid application protocol"))
		}
	})
	return err
}

// publishBulkMessageHTTP publishes bulk message to a subscriber using HTTP and takes care of corresponding response.
func (a *DaprRuntime) publishBulkMessageHTTP(ctx context.Context, msg *pubsubBulkSubscribedMessage,
	bulkResponses *[]pubsub.BulkSubscribeResponseEntry, entryIDIndexMap map[string]int,
) error {
	spans := make([]trace.Span, len(msg.entries))

	req := invokev1.NewInvokeMethodRequest(msg.path)
	req.WithHTTPExtension(nethttp.MethodPost, "")
	req.WithRawData(msg.data, contenttype.CloudEventContentType)
	req.WithCustomHTTPMetadata(msg.metadata)

	for i, cloudEvent := range msg.cloudEvents {
		if cloudEvent[pubsub.TraceIDField] != nil {
			traceID := cloudEvent[pubsub.TraceIDField].(string)
			sc, _ := diag.SpanContextFromW3CString(traceID)
			spanName := fmt.Sprintf("pubsub/%s", msg.topic)
			var span trace.Span
			ctx, span = diag.StartInternalCallbackSpan(ctx, spanName, sc, a.globalConfig.Spec.TracingSpec)
			spans[i] = span
		}
	}
	defer endSpans(spans)
	start := time.Now()
	resp, err := a.appChannel.InvokeMethod(ctx, req)
	elapsed := diag.ElapsedSince(start)

	if err != nil {
		diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
		populateBulkSubscribeResponsesWithError(msg.entries, bulkResponses, &entryIDIndexMap, err)
		return errors.Wrap(err, "error from app channel while sending pub/sub event to app")
	}

	statusCode := int(resp.Status().Code)

	for _, span := range spans {
		if span != nil {
			m := diag.ConstructSubscriptionSpanAttributes(msg.topic)
			diag.AddAttributesToSpan(span, m)
			diag.UpdateSpanStatusFromHTTPStatus(span, statusCode)
		}
	}

	_, body := resp.RawData()

	if (statusCode >= 200) && (statusCode <= 299) {
		// Any 2xx is considered a success.
		var appBulkResponse pubsub.AppBulkResponse
		err := json.Unmarshal(body, &appBulkResponse)
		if err != nil {
			diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Success)), msg.topic, elapsed)
			populateBulkSubscribeResponsesWithError(msg.entries, bulkResponses, &entryIDIndexMap, err)
			return errors.Wrap(err, "failed unmarshalling app response for bulk subscribe")
		}

		var hasAnyError bool
		for _, response := range appBulkResponse.AppResponses {
			if entryID, ok := entryIDIndexMap[response.EntryID]; ok {
				switch response.Status {
				case "":
					// When statusCode 2xx, Consider empty status field OR not receiving status for an item as retry
					fallthrough
				case pubsub.Retry:
					diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
					(*bulkResponses)[entryID].EntryID = response.EntryID
					(*bulkResponses)[entryID].Error = errors.Errorf("RETRY required while processing bulk subscribe event for entry id: %v", response.EntryID)
					hasAnyError = true
				case pubsub.Success:
					diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Success)), msg.topic, elapsed)
					(*bulkResponses)[entryID].EntryID = response.EntryID
					(*bulkResponses)[entryID].Error = nil
				case pubsub.Drop:
					diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Drop)), msg.topic, elapsed)
					log.Warnf("DROP status returned from app while processing pub/sub event %v", response.EntryID)
					(*bulkResponses)[entryID].EntryID = response.EntryID
					(*bulkResponses)[entryID].Error = nil
				default:
					// Consider unknown status field as error and retry
					diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
					(*bulkResponses)[entryID].EntryID = response.EntryID
					(*bulkResponses)[entryID].Error = errors.Errorf("unknown status returned from app while processing bulk subscribe event %v: %v", response.EntryID, response.Status)
					hasAnyError = true
				}
			} else {
				log.Warnf("Invalid entry id received from app while processing pub/sub event %v", response.EntryID)
				continue
			}
		}
		for _, item := range msg.entries {
			ind := entryIDIndexMap[item.EntryID]
			if (*bulkResponses)[ind].EntryID == "" {
				(*bulkResponses)[ind].EntryID = item.EntryID
				(*bulkResponses)[ind].Error = errors.Errorf("Response not received, RETRY required while processing bulk subscribe event for entry id: %v", item.EntryID)
			}
		}
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
		log.Errorf("non-retriable error returned from app while processing bulk pub/sub event: %s. status code returned: %v", body, statusCode)
		diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Drop)), msg.topic, elapsed)
		populateBulkSubscribeResponsesWithError(msg.entries, bulkResponses, &entryIDIndexMap, nil)
		return nil
	}

	// Every error from now on is a retriable error.
	log.Warnf("retriable error returned from app while processing bulk pub/sub event, topic: %v, body: %s. status code returned: %v", msg.topic, body, statusCode)
	diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, msg.pubsub, strings.ToLower(string(pubsub.Retry)), msg.topic, elapsed)
	populateBulkSubscribeResponsesWithError(msg.entries, bulkResponses, &entryIDIndexMap, errors.Errorf("retriable error returned from app while processing bulk pub/sub event, topic: %v, body: %s. status code returned: %v", msg.topic, body, statusCode))
	return errors.Errorf("retriable error returned from app while processing bulk pub/sub event, topic: %v, body: %s. status code returned: %v", msg.topic, body, statusCode)
}

// sendBulkToDeadLetter sends the bulk message to deadletter topic.
func (a *DaprRuntime) sendBulkToDeadLetter(
	ctx context.Context, name string, msg *pubsub.BulkMessage, deadLetterTopic string,
	entryIDIndexMap *map[string]int, bulkResponses *[]pubsub.BulkSubscribeResponseEntry,
) error {
	data := make([]pubsub.BulkMessageEntry, len(msg.Entries))

	if bulkResponses == nil {
		data = msg.Entries
	} else {
		n := 0
		for _, message := range msg.Entries {
			entryID := (*entryIDIndexMap)[message.EntryID]
			if (*bulkResponses)[entryID].Error != nil {
				data[n] = message
				n++
			}
		}
		data = data[:n]
	}

	req := &pubsub.BulkPublishRequest{
		Entries:    data,
		PubsubName: name,
		Topic:      deadLetterTopic,
		Metadata:   msg.Metadata,
	}

	_, err := a.BulkPublish(ctx, req)
	if err != nil {
		log.Errorf("error sending message to dead letter, origin topic: %s dead letter topic %s err: %w", msg.Topic, deadLetterTopic, err)
	}
	return err
}

func validateEntryID(entryID string, i int) error {
	if entryID == "" {
		log.Warn("Invalid blank entry id received while processing bulk pub/sub event, won't be able to process it")
		return errors.New("Blank entryID supplied - won't be able to process it")
	}
	return nil
}

func populateBulkSubcribedMessage(message *pubsub.BulkMessageEntry, event interface{},
	routePathBulkMessageMap *map[string]pubsubBulkSubscribedMessage,
	rPath string, i int, msg *pubsub.BulkMessage, isCloudEvent bool, psName string, contentType string,
) {
	childMessage := runtimePubsub.BulkSubscribeMessageItem{
		Event:       event,
		Metadata:    message.Metadata,
		EntryID:     message.EntryID,
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
		psm := pubsubBulkSubscribedMessage{
			cloudEvents: cloudEvents,
			rawData:     rawDataItems,
			entries:     entries,
			topic:       msg.Topic,
			metadata:    msg.Metadata,
			pubsub:      psName,
			length:      1,
		}
		(*routePathBulkMessageMap)[rPath] = psm
	}
}

func populateBulkSubscribeResponsesWithError(entries []*pubsub.BulkMessageEntry,
	bulkResponses *[]pubsub.BulkSubscribeResponseEntry, entryIDIndexMap *map[string]int, err error,
) {
	for _, item := range entries {
		ind := (*entryIDIndexMap)[item.EntryID]
		if (*bulkResponses)[ind].EntryID == "" {
			(*bulkResponses)[ind].EntryID = item.EntryID
			(*bulkResponses)[ind].Error = err
		}
	}
}
