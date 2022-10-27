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
	"github.com/dapr/dapr/utils"
)

const (
	bulkPublishSeriallyKey       string = "bulkPublishSerially"
	bulkPublishMaxConcurrencyKey string = "bulkPublishMaxConcurrency"

	defaultBulkPublishMaxConcurrency int = 100
)

var ErrBulkPublishFailure = errors.New("bulk publish failed")

// defaultBulkPublisher is the default implementation of BulkPublisher.
// It is used when the component does not implement BulkPublisher.
type defaultBulkPublisher struct {
	p contribPubsub.PubSub
}

// NewDefaultBulkPublisher returns a new defaultBulkPublisher from a PubSub.
func NewDefaultBulkPublisher(p contribPubsub.PubSub) *defaultBulkPublisher {
	return &defaultBulkPublisher{
		p: p,
	}
}

// BulkPublish publishes a list of messages to a topic as individual Publish requests.
// If 'bulkPublishSerially' metadata is set to true, the messages are sent to the broker serially,
// in the same order. Otherwise they are sent to the broker as parallel Publish requests.
func (p *defaultBulkPublisher) BulkPublish(_ context.Context, req *contribPubsub.BulkPublishRequest) (contribPubsub.BulkPublishResponse, error) {
	if utils.IsTruthy(req.Metadata[bulkPublishSeriallyKey]) {
		return p.bulkPublishSerial(req)
	} else {
		return p.bulkPublishParallel(req)
	}
}

// bulkPublishSerial publishes messages in a serial order. This is slower, but ensures
// that messages are sent to the broker in the same order as specified in the request.
func (p *defaultBulkPublisher) bulkPublishSerial(req *contribPubsub.BulkPublishRequest) (contribPubsub.BulkPublishResponse, error) {
	statuses := make([]contribPubsub.BulkPublishResponseEntry, 0, len(req.Entries))
	var err error

	for _, entry := range req.Entries {
		status := p.bulkPublishSingleEntry(req.PubsubName, req.Topic, entry)
		if status.Error != nil {
			err = ErrBulkPublishFailure
		}

		statuses = append(statuses, status)
	}

	return contribPubsub.BulkPublishResponse{Statuses: statuses}, err
}

// bulkPublishParallel publishes messages in parallel. This is faster, but does not guarantee
// that messages are sent to the broker in the same order as specified in the request.
func (p *defaultBulkPublisher) bulkPublishParallel(req *contribPubsub.BulkPublishRequest) (contribPubsub.BulkPublishResponse, error) {
	statuses := make([]contribPubsub.BulkPublishResponseEntry, 0, len(req.Entries))
	maxConcurrency := utils.GetIntOrDefault(req.Metadata, bulkPublishMaxConcurrencyKey, defaultBulkPublishMaxConcurrency)

	var eg errgroup.Group
	eg.SetLimit(maxConcurrency)

	statusChan := make(chan contribPubsub.BulkPublishResponseEntry, len(req.Entries))

	for i := range req.Entries {
		entry := req.Entries[i]
		eg.Go(func() error {
			status := p.bulkPublishSingleEntry(req.PubsubName, req.Topic, entry)
			statusChan <- status
			return status.Error
		})
	}

	err := eg.Wait()
	close(statusChan)

	for status := range statusChan {
		statuses = append(statuses, status)
	}

	return contribPubsub.BulkPublishResponse{Statuses: statuses}, err
}

// bulkPublishSingleEntry sends a single message to the broker as a Publish request.
func (p *defaultBulkPublisher) bulkPublishSingleEntry(pubsubName, topic string, entry contribPubsub.BulkMessageEntry) contribPubsub.BulkPublishResponseEntry {
	pr := contribPubsub.PublishRequest{
		Data:        entry.Event,
		PubsubName:  pubsubName,
		Topic:       topic,
		Metadata:    entry.Metadata,
		ContentType: &entry.ContentType,
	}

	if err := p.p.Publish(&pr); err != nil {
		return contribPubsub.BulkPublishResponseEntry{
			EntryId: entry.EntryId,
			Status:  contribPubsub.PublishFailed,
			Error:   err,
		}
	}

	return contribPubsub.BulkPublishResponseEntry{
		EntryId: entry.EntryId,
		Status:  contribPubsub.PublishSucceeded,
	}
}
