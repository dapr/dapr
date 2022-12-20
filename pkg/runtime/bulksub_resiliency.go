package runtime

import (
	"context"
	"sync/atomic"

	"github.com/cenkalti/backoff/v4"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/utils"
	"github.com/pkg/errors"
)

// ApplyBulkSubscribeResiliency applies resiliency support to bulk subscribe. It tries to filter
// out the messages that have been successfully processed and only retries the ones that have failed
func (a *DaprRuntime) ApplyBulkSubscribeResiliency(bulkSubCallData *bulkSubscribeCallData,
	psm pubsubBulkSubscribedMessage, deadLetterTopic string, path string, policyDef *resiliency.PolicyDefinition,
	rawPayload bool, envelope map[string]interface{},
) (*[]pubsub.BulkSubscribeResponseEntry, error) {
	bscData := *bulkSubCallData
	ctx := bscData.ctx
	var subscribedEntries atomic.Pointer[[]pubSubMessage]
	subscribedEntries.Store(&psm.pubSubMessages)
	policyRunner := resiliency.NewRunnerWithOptions(
		ctx, policyDef, resiliency.RunnerOpts[*[]pubsub.BulkSubscribeResponseEntry]{
			Accumulator: func(bsre *[]pubsub.BulkSubscribeResponseEntry) {
				for _, v := range *bsre {
					// add to main bulkResponses
					index := (*bscData.entryIdIndexMap)[v.EntryId]
					(*bscData.bulkResponses)[index].EntryId = v.EntryId
					(*bscData.bulkResponses)[index].Error = v.Error
				}
				filteredPubSubMsgs := utils.Filter(*subscribedEntries.Load(), func(ps pubSubMessage) bool {
					index := (*bscData.entryIdIndexMap)[ps.entry.EntryId]
					return (*bscData.bulkResponses)[index].Error != nil
				})
				psm.pubSubMessages = filteredPubSubMsgs
				psm.length = len(filteredPubSubMsgs)
			},
		})
	_, err := policyRunner(func(ctx context.Context) (*[]pubsub.BulkSubscribeResponseEntry, error) {
		var pErr error
		bsre := []pubsub.BulkSubscribeResponseEntry{}
		switch a.runtimeConfig.ApplicationProtocol {
		case HTTPProtocol:
			pErr = a.publishBulkMessageHTTP(&bscData, &psm, &bsre, envelope, deadLetterTopic)
		case GRPCProtocol:
			pErr = a.publishBulkMessageGRPC(&bscData, &psm, &bsre, rawPayload)
		default:
			pErr = backoff.Permanent(errors.New("invalid application protocol"))
		}
		return &bsre, pErr
	})
	// setting error if any entry has not been yet touched - only use case that seems possible is of timeout
	for eId, ind := range *bscData.entryIdIndexMap {
		if (*bscData.bulkResponses)[ind].EntryId == "" {
			(*bscData.bulkResponses)[ind].EntryId = eId
			(*bscData.bulkResponses)[ind].Error = err
		}
	}
	return bscData.bulkResponses, err
}
