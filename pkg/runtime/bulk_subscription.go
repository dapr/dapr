package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/cenkalti/backoff/v4"
	"github.com/dapr/components-contrib/contenttype"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/google/uuid"
	"github.com/pkg/errors"
)

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
			if route.deadLetterTopic != "" {
				if dlqErr := a.sendBulkToDeadLetter(psName, msg, route.deadLetterTopic, nil, nil); dlqErr == nil {
					// dlq has been configured and whole bulk of messages is successfully sent to dlq.
					diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msg.Topic, 0)
					return nil, nil
				}
			}
			diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msg.Topic, 0)
			return nil, err
		}

		cloudEvents := make([]map[string]interface{}, len(msg.Entries))
		routePathBulkMessageMap := make(map[string]pubsubBulkSubscribedMessage)
		bulkResponses := make([]pubsub.BulkSubscribeResponseEntry, len(msg.Entries))
		entryIDIndexMap := make(map[string]int)
		hasAnyError := false
		for i, message := range msg.Entries {
			if message.EntryID == "" {
				log.Warnf("Invalid entry id %v received while processing bulk pub/sub event, won't be able to process it", message.EntryID)
				continue
			}
			entryIDIndexMap[message.EntryID] = i
			if rawPayload {
				rPath, shouldProcess, routeErr := findMatchingRoute(route.rules, string(message.Event))
				if routeErr != nil {
					log.Errorf("error finding matching route for event in bulk subscribe %s and topic %s for entry id %s: %s", psName, topic, message.EntryID, err)
					bulkResponses[i].EntryID = message.EntryID
					bulkResponses[i].Error = err
					hasAnyError = true
					continue
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
					bulkResponses[i].EntryID = message.EntryID
					bulkResponses[i].Error = nil
					continue
				}
				dataB64 := base64.StdEncoding.EncodeToString(message.Event)
				childMessage := runtimePubsub.BulkSubscribeMessageItem{
					Event:       dataB64,
					Metadata:    message.Metadata,
					EntryID:     message.EntryID,
					ContentType: "application/octet-stream",
				}
				if val, ok := routePathBulkMessageMap[rPath]; ok {
					val.rawData[val.length] = childMessage
					val.entries[val.length] = &msg.Entries[i]
					val.length++
					routePathBulkMessageMap[rPath] = val
				} else {
					rawDataItems := make([]runtimePubsub.BulkSubscribeMessageItem, len(msg.Entries))
					rawDataItems[0] = childMessage
					entries := make([]*pubsub.BulkMessageEntry, len(msg.Entries))
					entries[0] = &msg.Entries[i]
					psm := pubsubBulkSubscribedMessage{
						cloudEvents: cloudEvents,
						rawData:     rawDataItems,
						entries:     entries,
						topic:       msg.Topic,
						metadata:    msg.Metadata,
						pubsub:      psName,
						length:      1,
					}
					routePathBulkMessageMap[rPath] = psm
				}
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
				rPath, shouldProcess, routeErr := findMatchingRoute(route.rules, cloudEvent)
				if routeErr != nil {
					log.Errorf("error finding matching route for event %v in pubsub %s and topic %s: %s", cloudEvent[pubsub.IDField], psName, topic, err)
					bulkResponses[i].Error = err
					bulkResponses[i].EntryID = message.EntryID
					hasAnyError = true
					continue
				}
				if !shouldProcess {
					// The event does not match any route specified so ignore it.
					log.Debugf("no matching route for event %v in pubsub %s and topic %s; skipping", cloudEvent[pubsub.IDField], psName, topic)
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
				childMessage := runtimePubsub.BulkSubscribeMessageItem{
					Event:       cloudEvent,
					Metadata:    message.Metadata,
					EntryID:     message.EntryID,
					ContentType: contenttype.CloudEventContentType,
				}

				if val, ok := routePathBulkMessageMap[rPath]; ok {
					val.cloudEvents[val.length] = cloudEvent
					val.rawData[val.length] = childMessage
					val.entries[val.length] = &msg.Entries[i]
					val.length++
					routePathBulkMessageMap[rPath] = val
				} else {
					rawDataItems := make([]runtimePubsub.BulkSubscribeMessageItem, len(msg.Entries))
					rawDataItems[0] = childMessage
					entries := make([]*pubsub.BulkMessageEntry, len(msg.Entries))
					entries[0] = &msg.Entries[i]
					cloudEvents[0] = cloudEvent
					psm := pubsubBulkSubscribedMessage{
						cloudEvents: cloudEvents,
						rawData:     rawDataItems,
						entries:     entries,
						topic:       msg.Topic,
						metadata:    msg.Metadata,
						pubsub:      psName,
						length:      1,
					}
					routePathBulkMessageMap[rPath] = psm
				}
			}
		}
		for path, psm := range routePathBulkMessageMap {
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
				log.Errorf("error serializing bulk cloud event in pubsub %s and topic %s: %s", psName, msg.Topic, err)
				if route.deadLetterTopic != "" {
					ent := make([]pubsub.BulkMessageEntry, 0)
					for _, entry := range psm.entries {
						ent = append(ent, *entry)
					}
					bulkMsg := pubsub.BulkMessage{
						Entries:  ent,
						Topic:    msg.Topic,
						Metadata: msg.Metadata,
					}
					if dlqErr := a.sendBulkToDeadLetter(psName, &bulkMsg, route.deadLetterTopic, &entryIDIndexMap, nil); dlqErr == nil {
						// dlq has been configured and message is successfully sent to dlq.
						diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msg.Topic, 0)
						for _, item := range psm.entries {
							ind := entryIDIndexMap[item.EntryID]
							bulkResponses[ind].EntryID = item.EntryID
							bulkResponses[ind].Error = nil
						}
						continue
					}
				}
				diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msg.Topic, 0)
				for _, item := range psm.entries {
					ind := entryIDIndexMap[item.EntryID]
					bulkResponses[ind].EntryID = item.EntryID
					bulkResponses[ind].Error = err
				}
				continue
			}
			psm.data = da
			psm.path = path
			err = policy(func(ctx context.Context) error {
				switch a.runtimeConfig.ApplicationProtocol {
				case HTTPProtocol:
					psm := psm
					errPub := a.publishBulkMessageHTTP(ctx, &psm, &bulkResponses, entryIDIndexMap)
					return errPub
				default:
					return backoff.Permanent(errors.New("invalid application protocol"))
				}
			})
		}
		if (err != nil || hasAnyError) && err != context.Canceled {
			// Sending msg to dead letter queue.
			// If no DLQ is configured, return error for backwards compatibility (component-level retry).
			if route.deadLetterTopic != "" {
				if dlqErr := a.sendBulkToDeadLetter(psName, msg, route.deadLetterTopic, &entryIDIndexMap, &bulkResponses); dlqErr == nil {
					// dlq has been configured and message is successfully sent to dlq.
					diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Drop)), msg.Topic, 0)
					return nil, nil
				}
			}
			diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, pubsubName, strings.ToLower(string(pubsub.Retry)), msg.Topic, 0)
			return bulkResponses, err
		}
		return bulkResponses, err
	}

	if bulkSubscriber, ok := ps.component.(pubsub.BulkSubscriber); ok {
		return bulkSubscriber.BulkSubscribe(ctx, req, bulkHandler)
	}

	return runtimePubsub.NewDefaultBulkSubscriber(ps.component).BulkSubscribe(ctx, req, bulkHandler)
}
