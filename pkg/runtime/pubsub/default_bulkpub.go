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
	"errors"

	"golang.org/x/sync/errgroup"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
)

const (
	defaultBulkPublishMaxConcurrency int = 100
)

var ErrBulkPublishFailure = errors.New("bulk publish failed")

// defaultBulkPublisher is the default implementation of BulkPublisher.
// It is used when the component does not implement BulkPublisher.
type defaultBulkPublisher struct {
	p contribPubsub.PubSub
}

// NewDefaultBulkPublisher returns a new defaultBulkPublisher from a PubSub.
func NewDefaultBulkPublisher(p contribPubsub.PubSub) contribPubsub.BulkPublisher {
	return &defaultBulkPublisher{
		p: p,
	}
}

// BulkPublish publishes a list of messages as parallel Publish requests to the topic in the incoming request.
// There is no guarantee that messages sent to the broker are in the same order as specified in the request.
func (p *defaultBulkPublisher) BulkPublish(ctx context.Context, req *contribPubsub.BulkPublishRequest) (contribPubsub.BulkPublishResponse, error) {
	failedEntries := make([]contribPubsub.BulkPublishResponseFailedEntry, 0, len(req.Entries))

	var eg errgroup.Group
	eg.SetLimit(defaultBulkPublishMaxConcurrency)

	faileEntryChan := make(chan contribPubsub.BulkPublishResponseFailedEntry, len(req.Entries))

	for i := range req.Entries {
		entry := req.Entries[i]
		eg.Go(func() error {
			failedEntry := p.bulkPublishSingleEntry(ctx, req.PubsubName, req.Topic, entry)
			if failedEntry != nil {
				faileEntryChan <- *failedEntry
				return failedEntry.Error
			}
			return nil
		})
	}

	err := eg.Wait()
	close(faileEntryChan)

	for entry := range faileEntryChan {
		failedEntries = append(failedEntries, entry)
	}

	return contribPubsub.BulkPublishResponse{FailedEntries: failedEntries}, err
}

// bulkPublishSingleEntry sends a single message to the broker as a Publish request.
func (p *defaultBulkPublisher) bulkPublishSingleEntry(ctx context.Context, pubsubName, topic string, entry contribPubsub.BulkMessageEntry) *contribPubsub.BulkPublishResponseFailedEntry {
	pr := contribPubsub.PublishRequest{
		Data:        entry.Event,
		PubsubName:  pubsubName,
		Topic:       topic,
		Metadata:    entry.Metadata,
		ContentType: &entry.ContentType,
	}

	if err := p.p.Publish(ctx, &pr); err != nil {
		return &contribPubsub.BulkPublishResponseFailedEntry{
			EntryId: entry.EntryId,
			Error:   err,
		}
	}

	return nil
}
