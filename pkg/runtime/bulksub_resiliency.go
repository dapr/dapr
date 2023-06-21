package runtime

import (
	"context"

	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/utils"
)

// ApplyBulkSubscribeResiliency applies resiliency support to bulk subscribe. It tries to filter
// out the messages that have been successfully processed and only retries the ones that have failed
func (a *DaprRuntime) ApplyBulkSubscribeResiliency(ctx context.Context, bulkSubCallData *bulkSubscribeCallData,
	psm pubsubBulkSubscribedMessage, deadLetterTopic string, path string, policyDef *resiliency.PolicyDefinition,
	rawPayload bool, envelope map[string]interface{},
) (*[]pubsub.BulkSubscribeResponseEntry, error) {
	bscData := *bulkSubCallData
	policyRunner := resiliency.NewRunnerWithOptions(
		ctx, policyDef, resiliency.RunnerOpts[*[]pubsub.BulkSubscribeResponseEntry]{
			Accumulator: func(bsre *[]pubsub.BulkSubscribeResponseEntry) {
				for _, v := range *bsre {
					// add to main bulkResponses
					if index, ok := (*bscData.entryIdIndexMap)[v.EntryId]; ok {
						(*bscData.bulkResponses)[index].EntryId = v.EntryId
						(*bscData.bulkResponses)[index].Error = v.Error
					}
				}
				filteredPubSubMsgs := utils.Filter(psm.pubSubMessages, func(ps pubSubMessage) bool {
					if index, ok := (*bscData.entryIdIndexMap)[ps.entry.EntryId]; ok {
						return (*bscData.bulkResponses)[index].Error != nil
					}
					return false
				})
				psm.pubSubMessages = filteredPubSubMsgs
				psm.length = len(filteredPubSubMsgs)
			},
		})
	_, err := policyRunner(func(ctx context.Context) (*[]pubsub.BulkSubscribeResponseEntry, error) {
		var pErr error
		bsre := []pubsub.BulkSubscribeResponseEntry{}
		if a.runtimeConfig.AppConnectionConfig.Protocol.IsHTTP() {
			pErr = a.publishBulkMessageHTTP(ctx, &bscData, &psm, &bsre, envelope, deadLetterTopic)
		} else {
			pErr = a.publishBulkMessageGRPC(ctx, &bscData, &psm, &bsre, rawPayload)
		}
		return &bsre, pErr
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
