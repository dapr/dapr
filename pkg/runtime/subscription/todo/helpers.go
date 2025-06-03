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

package todo

import (
	"context"
	"errors"
	"strings"

	"go.opentelemetry.io/otel/trace"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.processor.pubsub.subscription.todo")

func ValidateEntryId(entryId string, i int) error { //nolint:stylecheck
	if entryId == "" {
		log.Warn("Invalid blank entry id received while processing bulk pub/sub event, won't be able to process it")
		//nolint:stylecheck
		return errors.New("Blank entryId supplied - won't be able to process it")
	}
	return nil
}

func PopulateBulkSubcribedMessage(msgE *contribpubsub.BulkMessageEntry, event interface{},
	routePathBulkMessageMap *map[string]BulkSubscribedMessage,
	rPath string, i int, msg *contribpubsub.BulkMessage, isCloudEvent bool, psName string, contentType string, namespacedConsumer bool, namespace string,
) {
	childMessage := rtpubsub.BulkSubscribeMessageItem{
		Event:       event,
		Metadata:    msgE.Metadata,
		EntryId:     msgE.EntryId,
		ContentType: contentType,
	}
	var cloudEvent map[string]interface{}
	mapTypeEvent, ok := event.(map[string]interface{})
	if ok {
		cloudEvent = mapTypeEvent
	}
	if val, ok := (*routePathBulkMessageMap)[rPath]; ok {
		if isCloudEvent {
			val.PubSubMessages[val.Length].CloudEvent = mapTypeEvent
		}
		val.PubSubMessages[val.Length].RawData = &childMessage
		val.PubSubMessages[val.Length].Entry = &msg.Entries[i]
		val.Length++
		(*routePathBulkMessageMap)[rPath] = val
	} else {
		pubSubMessages := make([]Message, len(msg.Entries))
		pubSubMessages[0].RawData = &childMessage
		pubSubMessages[0].Entry = &msg.Entries[i]
		if isCloudEvent {
			pubSubMessages[0].CloudEvent = cloudEvent
		}

		msgTopic := msg.Topic
		if namespacedConsumer {
			msgTopic = strings.Replace(msgTopic, namespace, "", 1)
		}

		psm := BulkSubscribedMessage{
			PubSubMessages: pubSubMessages,
			Topic:          msgTopic,
			Metadata:       msg.Metadata,
			Pubsub:         psName,
			Length:         1,
		}
		(*routePathBulkMessageMap)[rPath] = psm
	}
}

func PopulateBulkSubscribeResponsesWithError(psm *BulkSubscribedMessage,
	bulkResponses *[]contribpubsub.BulkSubscribeResponseEntry, err error,
) {
	for _, message := range psm.PubSubMessages {
		AddBulkResponseEntry(bulkResponses, message.Entry.EntryId, err)
	}
}

func PopulateAllBulkResponsesWithError(bulkMsg *contribpubsub.BulkMessage,
	bulkResponses *[]contribpubsub.BulkSubscribeResponseEntry, err error,
) {
	for i, item := range bulkMsg.Entries {
		if (*bulkResponses)[i].EntryId == "" {
			SetBulkResponseEntry(bulkResponses, i, item.EntryId, err)
		}
	}
}

func SetBulkResponseEntry(bulkResponses *[]contribpubsub.BulkSubscribeResponseEntry, i int, entryId string, err error) { //nolint:stylecheck
	(*bulkResponses)[i].EntryId = entryId
	(*bulkResponses)[i].Error = err
}

func AddBulkResponseEntry(bulkResponses *[]contribpubsub.BulkSubscribeResponseEntry, entryId string, err error) { //nolint:stylecheck
	resp := contribpubsub.BulkSubscribeResponseEntry{
		EntryId: entryId,
		Error:   err,
	}
	*bulkResponses = append(*bulkResponses, resp)
}

func NewBulkSubIngressDiagnostics() BulkSubIngressDiagnostics {
	statusWiseCountDiag := make(map[string]int64, 3)
	statusWiseCountDiag[string(contribpubsub.Success)] = 0
	statusWiseCountDiag[string(contribpubsub.Drop)] = 0
	statusWiseCountDiag[string(contribpubsub.Retry)] = 0
	bulkSubDiag := BulkSubIngressDiagnostics{
		StatusWiseDiag: statusWiseCountDiag,
		Elapsed:        0,
		RetryReported:  false,
	}
	return bulkSubDiag
}

func ReportBulkSubDiagnostics(ctx context.Context, topic string, bulkSubDiag *BulkSubIngressDiagnostics) {
	if bulkSubDiag == nil {
		return
	}
	diag.DefaultComponentMonitoring.BulkPubsubIngressEvent(ctx, rtpubsub.MetadataKeyPubSub, topic, bulkSubDiag.Elapsed)
	for status, count := range bulkSubDiag.StatusWiseDiag {
		diag.DefaultComponentMonitoring.BulkPubsubIngressEventEntries(ctx, rtpubsub.MetadataKeyPubSub, topic, status, count)
	}
}

func EndSpans(spans []trace.Span) {
	for _, span := range spans {
		if span != nil {
			span.End()
		}
	}
}
