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

package subscription

import (
	"context"
	"maps"
	"sync"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman"
	"github.com/dapr/dapr/pkg/runtime/subscription/todo"
	"github.com/dapr/dapr/utils"
)

// applyBulkSubscribeResiliency applies resiliency support to bulk subscribe. It tries to filter
// out the messages that have been successfully processed and only retries the ones that have failed
func (s *Subscription) applyBulkSubscribeResiliency(ctx context.Context, bulkSubCallData *todo.BulkSubscribeCallData,
	psm todo.BulkSubscribedMessage, deadLetterTopic string, path string, policyDef *resiliency.PolicyDefinition,
	rawPayload bool, envelope map[string]any,
) (*[]contribpubsub.BulkSubscribeResponseEntry, error) {
	bscData := *bulkSubCallData

	// pending holds the entries still to be delivered, which the accumulator
	// narrows to the ones that failed before the next attempt runs. A timed out
	// attempt is abandoned rather than stopped, so it can still be delivering
	// while the attempt that replaced it is accumulated: guard pending, and give
	// every attempt its own message, so that the two never share one.
	var pendingLock sync.Mutex
	pending := psm.PubSubMessages

	policyRunner := resiliency.NewRunnerWithOptions(
		ctx, policyDef, resiliency.RunnerOpts[*todo.BulkSubscribeResiliencyRes]{
			Accumulator: func(bsrr *todo.BulkSubscribeResiliencyRes) {
				for _, v := range bsrr.Entries {
					// add to main bulkResponses
					if index, ok := (*bscData.EntryIdIndexMap)[v.EntryId]; ok {
						(*bscData.BulkResponses)[index].EntryId = v.EntryId
						(*bscData.BulkResponses)[index].Error = v.Error
					}
				}

				pendingLock.Lock()
				defer pendingLock.Unlock()

				// utils.Filter returns a new slice, so an attempt still working
				// through an earlier one is left untouched by this.
				pending = utils.Filter(pending, func(ps todo.Message) bool {
					if index, ok := (*bscData.EntryIdIndexMap)[ps.Entry.EntryId]; ok {
						return (*bscData.BulkResponses)[index].Error != nil
					}

					return false
				})
			},
		})
	_, err := policyRunner(func(ctx context.Context) (*todo.BulkSubscribeResiliencyRes, error) {
		pendingLock.Lock()
		attempt := psm
		attempt.PubSubMessages = pending
		pendingLock.Unlock()
		attempt.Length = len(attempt.PubSubMessages)

		bsrr := &todo.BulkSubscribeResiliencyRes{
			Entries:  make([]contribpubsub.BulkSubscribeResponseEntry, 0, len(attempt.PubSubMessages)),
			Envelope: maps.Clone(envelope),
		}
		err := s.postman.DeliverBulk(ctx, &postman.DeliverBulkRequest{
			BulkSubCallData:      &bscData,
			BulkSubMsg:           &attempt,
			BulkSubResiliencyRes: bsrr,
			BulkResponses:        &bsrr.Entries,
			RawPayload:           rawPayload,
			DeadLetterTopic:      deadLetterTopic,
		})

		return bsrr, err
	})
	// setting error if any entry has not been yet touched - only use case that seems possible is of timeout
	for eId, ind := range *bscData.EntryIdIndexMap { //nolint:stylecheck
		if (*bscData.BulkResponses)[ind].EntryId == "" {
			(*bscData.BulkResponses)[ind].EntryId = eId
			(*bscData.BulkResponses)[ind].Error = err
		}
	}

	return bscData.BulkResponses, err
}
