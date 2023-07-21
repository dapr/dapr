/*
Copyright 2022 The Dapr Authors
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
	"sync/atomic"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/utils"
)

func ApplyBulkPublishResiliency(ctx context.Context, req *contribPubsub.BulkPublishRequest,
	policyDef *resiliency.PolicyDefinition,
	bulkPublisher contribPubsub.BulkPublisher,
) (contribPubsub.BulkPublishResponse, error) {
	// Contains the latest request entries to be sent to the component
	var requestEntries atomic.Pointer[[]contribPubsub.BulkMessageEntry]
	requestEntries.Store(&req.Entries)
	policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef,
		resiliency.RunnerOpts[contribPubsub.BulkPublishResponse]{
			Accumulator: func(res contribPubsub.BulkPublishResponse) {
				if len(res.FailedEntries) == 0 {
					return
				}

				// requestEntries can be modified here as Accumulator is executed synchronously
				failedEntryIds := extractEntryIds(res.FailedEntries)
				filteredEntries := utils.Filter(*requestEntries.Load(), func(item contribPubsub.BulkMessageEntry) bool {
					_, ok := failedEntryIds[item.EntryId]
					return ok
				})
				requestEntries.Store(&filteredEntries)
			},
		})
	res, err := policyRunner(func(ctx context.Context) (contribPubsub.BulkPublishResponse, error) {
		newEntries := *requestEntries.Load()
		newReq := &contribPubsub.BulkPublishRequest{
			PubsubName: req.PubsubName,
			Topic:      req.Topic,
			Entries:    newEntries,
			Metadata:   req.Metadata,
		}
		return bulkPublisher.BulkPublish(ctx, newReq)
	})
	// If final error is timeout, CB open or CB too many requests, return the current request entries as failed
	if err != nil &&
		(len(res.FailedEntries) == 0 ||
			resiliency.IsTimeoutExeceeded(err) ||
			resiliency.IsCircuitBreakerError(err)) {
		return contribPubsub.NewBulkPublishResponse(*requestEntries.Load(), err), err
	}
	// Otherwise, retry has exhausted, return the final response got from the bulk publisher
	return res, err
}

func extractEntryIds(failedEntries []contribPubsub.BulkPublishResponseFailedEntry) map[string]struct{} {
	entryIds := make(map[string]struct{}, len(failedEntries))
	for _, failedEntry := range failedEntries {
		entryIds[failedEntry.EntryId] = struct{}{}
	}
	return entryIds
}
