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
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/dapr/components-contrib/contenttype"
	"github.com/dapr/components-contrib/metadata"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/resiliency"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/todo"
)

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
func (s *Subscription) bulkSubscribeTopic(ctx context.Context, policyDef *resiliency.PolicyDefinition) error {
	subscribeTopic := s.topic
	psName := s.pubsubName
	route := s.route
	topic := s.topic
	namespacedConsumer := s.pubsub.NamespaceScoped
	if namespacedConsumer {
		subscribeTopic = s.namespace + s.topic
	}

	req := contribpubsub.SubscribeRequest{
		Topic:    subscribeTopic,
		Metadata: s.route.Metadata,
		BulkSubscribeConfig: contribpubsub.BulkSubscribeConfig{
			MaxMessagesCount:   int(s.route.BulkSubscribe.MaxMessagesCount),
			MaxAwaitDurationMs: int(s.route.BulkSubscribe.MaxAwaitDurationMs),
		},
	}

	bulkHandler := func(ctx context.Context, msg *contribpubsub.BulkMessage) ([]contribpubsub.BulkSubscribeResponseEntry, error) {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]string, 1)
		}

		msg.Metadata[rtpubsub.MetadataKeyPubSub] = s.pubsubName
		bulkSubDiag := todo.NewBulkSubIngressDiagnostics()
		bulkResponses := make([]contribpubsub.BulkSubscribeResponseEntry, len(msg.Entries))
		routePathBulkMessageMap := make(map[string]todo.BulkSubscribedMessage)
		entryIdIndexMap := make(map[string]int, len(msg.Entries)) //nolint:stylecheck
		bulkSubCallData := todo.BulkSubscribeCallData{
			BulkResponses:   &bulkResponses,
			BulkSubDiag:     &bulkSubDiag,
			EntryIdIndexMap: &entryIdIndexMap,
			PsName:          psName,
			Topic:           topic,
		}
		rawPayload, err := metadata.IsRawPayload(route.Metadata)
		if err != nil {
			log.Errorf("error deserializing pubsub metadata: %s", err)
			if dlqErr := s.sendBulkToDLQIfConfigured(ctx, &bulkSubCallData, msg, true, route); dlqErr != nil {
				todo.PopulateAllBulkResponsesWithError(msg, &bulkResponses, err)
				todo.ReportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
				return bulkResponses, err
			}
			todo.ReportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
			return nil, nil
		}
		hasAnyError := false
		for i, message := range msg.Entries {
			if entryIdErr := todo.ValidateEntryId(message.EntryId, i); entryIdErr != nil { //nolint:stylecheck
				bulkResponses[i].Error = entryIdErr
				hasAnyError = true
				continue
			}
			entryIdIndexMap[message.EntryId] = i
			if rawPayload {
				rPath, routeErr := s.getRouteIfProcessable(ctx, &bulkSubCallData, route, &(msg.Entries[i]), i, string(message.Event))
				if routeErr != nil {
					hasAnyError = true
					continue
				}
				dataB64 := base64.StdEncoding.EncodeToString(message.Event)
				if message.ContentType == "" {
					message.ContentType = "application/octet-stream"
				}
				todo.PopulateBulkSubcribedMessage(&(msg.Entries[i]), dataB64, &routePathBulkMessageMap, rPath, i, msg, false, psName, message.ContentType, namespacedConsumer, s.namespace)
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
				if contribpubsub.HasExpired(cloudEvent) {
					log.Warnf("dropping expired pub/sub event %v as of %v", cloudEvent[contribpubsub.IDField], cloudEvent[contribpubsub.ExpirationField])
					bulkSubDiag.StatusWiseDiag[string(contribpubsub.Drop)]++
					if route.DeadLetterTopic != "" {
						_ = s.sendToDeadLetter(ctx, psName, &contribpubsub.NewMessage{
							Data:        message.Event,
							Topic:       topic,
							Metadata:    message.Metadata,
							ContentType: &msg.Entries[i].ContentType,
						}, route.DeadLetterTopic)
					}
					bulkResponses[i].EntryId = message.EntryId
					bulkResponses[i].Error = nil
					continue
				}
				rPath, routeErr := s.getRouteIfProcessable(ctx, &bulkSubCallData, route, &(msg.Entries[i]), i, cloudEvent)
				if routeErr != nil {
					hasAnyError = true
					continue
				}
				if message.ContentType == "" {
					message.ContentType = contenttype.CloudEventContentType
				}
				todo.PopulateBulkSubcribedMessage(&(msg.Entries[i]), cloudEvent, &routePathBulkMessageMap, rPath, i, msg, true, psName, message.ContentType, namespacedConsumer, s.namespace)
			}
		}
		var overallInvokeErr error
		for path, psm := range routePathBulkMessageMap {
			invokeErr := s.createEnvelopeAndInvokeSubscriber(ctx, &bulkSubCallData, psm, msg, route, path, policyDef, rawPayload)
			if invokeErr != nil {
				hasAnyError = true
				err = invokeErr
				overallInvokeErr = invokeErr
			}
		}
		if errors.Is(overallInvokeErr, context.Canceled) {
			todo.ReportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
			return bulkResponses, overallInvokeErr
		}
		if hasAnyError {
			// Sending msg to dead letter queue.
			// If no DLQ is configured, return error for backwards compatibility (component-level retry).
			bulkSubDiag.RetryReported = true
			if dlqErr := s.sendBulkToDLQIfConfigured(ctx, &bulkSubCallData, msg, false, route); dlqErr != nil {
				todo.ReportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
				return bulkResponses, err
			}
			todo.ReportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
			return nil, nil
		}
		todo.ReportBulkSubDiagnostics(ctx, topic, &bulkSubDiag)
		return bulkResponses, err
	}

	if bulkSubscriber, ok := s.pubsub.Component.(contribpubsub.BulkSubscriber); ok {
		return bulkSubscriber.BulkSubscribe(ctx, req, bulkHandler)
	}

	return rtpubsub.NewDefaultBulkSubscriber(s.pubsub.Component).BulkSubscribe(ctx, req, bulkHandler)
}

// sendBulkToDLQIfConfigured sends the message to the dead letter queue if configured.
func (s *Subscription) sendBulkToDLQIfConfigured(ctx context.Context, bulkSubCallData *todo.BulkSubscribeCallData, msg *contribpubsub.BulkMessage,
	sendAllEntries bool, route rtpubsub.Subscription,
) error {
	bscData := *bulkSubCallData
	if route.DeadLetterTopic != "" {
		if dlqErr := s.sendBulkToDeadLetter(ctx, bulkSubCallData, msg, route.DeadLetterTopic, sendAllEntries); dlqErr == nil {
			// dlq has been configured and whole bulk of messages is successfully sent to dlq.
			return nil
		}
	}
	if !bscData.BulkSubDiag.RetryReported {
		bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Retry)] += int64(len(msg.Entries))
	}
	return errors.New("failed to send to DLQ as DLQ was not configured")
}

// getRouteIfProcessable returns the route path if the message is processable.
func (s *Subscription) getRouteIfProcessable(ctx context.Context, bulkSubCallData *todo.BulkSubscribeCallData, route rtpubsub.Subscription, message *contribpubsub.BulkMessageEntry,
	i int, matchElem interface{},
) (string, error) {
	bscData := *bulkSubCallData
	rPath, shouldProcess, routeErr := findMatchingRoute(route.Rules, matchElem)
	if routeErr != nil {
		log.Errorf("Error finding matching route for event in bulk subscribe %s and topic %s for entry id %s: %s", bscData.PsName, bscData.Topic, message.EntryId, routeErr)
		todo.SetBulkResponseEntry(bscData.BulkResponses, i, message.EntryId, routeErr)
		return "", routeErr
	}
	if !shouldProcess {
		// The event does not match any route specified so ignore it.
		log.Warnf("No matching route for event in pubsub %s and topic %s; skipping", bscData.PsName, bscData.Topic)
		bscData.BulkSubDiag.StatusWiseDiag[string(contribpubsub.Drop)]++
		if route.DeadLetterTopic != "" {
			_ = s.sendToDeadLetter(ctx, bscData.PsName, &contribpubsub.NewMessage{
				Data:        message.Event,
				Topic:       bscData.Topic,
				Metadata:    message.Metadata,
				ContentType: &message.ContentType,
			}, route.DeadLetterTopic)
		}
		todo.SetBulkResponseEntry(bscData.BulkResponses, i, message.EntryId, nil)
		return "", nil
	}
	return rPath, nil
}

// createEnvelopeAndInvokeSubscriber creates the envelope and invokes the subscriber.
func (s *Subscription) createEnvelopeAndInvokeSubscriber(ctx context.Context, bulkSubCallData *todo.BulkSubscribeCallData, psm todo.BulkSubscribedMessage,
	msg *contribpubsub.BulkMessage, route rtpubsub.Subscription, path string, policyDef *resiliency.PolicyDefinition,
	rawPayload bool,
) error {
	bscData := *bulkSubCallData
	var id string
	idObj, err := uuid.NewRandom()
	if err != nil {
		id = idObj.String()
	}
	psm.PubSubMessages = psm.PubSubMessages[:psm.Length]
	psm.Path = path
	envelope := rtpubsub.NewBulkSubscribeEnvelope(&rtpubsub.BulkSubscribeEnvelope{
		ID:       id,
		Topic:    bscData.Topic,
		Pubsub:   bscData.PsName,
		Metadata: msg.Metadata,
	})
	_, e := s.applyBulkSubscribeResiliency(ctx, bulkSubCallData, psm, route.DeadLetterTopic,
		path, policyDef, rawPayload, envelope)
	return e
}

// sendBulkToDeadLetter sends the bulk message to deadletter topic.
func (s *Subscription) sendBulkToDeadLetter(ctx context.Context,
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

	_, err := s.adapter.BulkPublish(ctx, req)
	if err != nil {
		log.Errorf("error sending message to dead letter, origin topic: %s dead letter topic %s err: %w", msg.Topic, deadLetterTopic, err)
	}

	return err
}
