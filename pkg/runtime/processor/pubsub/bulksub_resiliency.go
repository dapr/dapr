/*
Copyright 2023 The Dapr Authors
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

package pubsub

import (
	"context"

	"golang.org/x/exp/maps"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/utils"
)

type bulkSubscribeResiliencyRes struct {
	entries  []contribpubsub.BulkSubscribeResponseEntry
	envelope map[string]interface{}
}

// applyBulkSubscribeResiliency applies resiliency support to bulk subscribe. It tries to filter
// out the messages that have been successfully processed and only retries the ones that have failed
func (p *pubsub) applyBulkSubscribeResiliency(ctx context.Context, bulkSubCallData *bulkSubscribeCallData,
	psm bulkSubscribedMessage, deadLetterTopic string, path string, policyDef *resiliency.PolicyDefinition,
	rawPayload bool, envelope map[string]interface{},
) (*[]contribpubsub.BulkSubscribeResponseEntry, error) {
	bscData := *bulkSubCallData
	policyRunner := resiliency.NewRunnerWithOptions(
		ctx, policyDef, resiliency.RunnerOpts[*bulkSubscribeResiliencyRes]{
			Accumulator: func(bsrr *bulkSubscribeResiliencyRes) {
				for _, v := range bsrr.entries {
					// add to main bulkResponses
					if index, ok := (*bscData.entryIdIndexMap)[v.EntryId]; ok {
						(*bscData.bulkResponses)[index].EntryId = v.EntryId
						(*bscData.bulkResponses)[index].Error = v.Error
					}
				}
				filteredPubSubMsgs := utils.Filter(psm.pubSubMessages, func(ps message) bool {
					if index, ok := (*bscData.entryIdIndexMap)[ps.entry.EntryId]; ok {
						return (*bscData.bulkResponses)[index].Error != nil
					}
					return false
				})
				psm.pubSubMessages = filteredPubSubMsgs
				psm.length = len(filteredPubSubMsgs)
			},
		})
	_, err := policyRunner(func(ctx context.Context) (*bulkSubscribeResiliencyRes, error) {
		var pErr error
		bsrr := &bulkSubscribeResiliencyRes{
			entries:  make([]contribpubsub.BulkSubscribeResponseEntry, 0, len(psm.pubSubMessages)),
			envelope: maps.Clone(envelope),
		}
		if p.isHTTP {
			pErr = p.publishBulkMessageHTTP(ctx, &bscData, &psm, bsrr, deadLetterTopic)
		} else {
			pErr = p.publishBulkMessageGRPC(ctx, &bscData, &psm, &bsrr.entries, rawPayload, deadLetterTopic)
		}
		return bsrr, pErr
	})
	// setting error if any entry has not been yet touched - only use case that seems possible is of timeout
	for eId, ind := range *bscData.entryIdIndexMap { //nolint:stylecheck
		if (*bscData.bulkResponses)[ind].EntryId == "" {
			(*bscData.bulkResponses)[ind].EntryId = eId
			(*bscData.bulkResponses)[ind].Error = err
		}
	}
	return bscData.bulkResponses, err
}
