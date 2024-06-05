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

package fake

import (
	"context"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
)

// Fake is a fake publisher
type Fake struct {
	publishFn     func(context.Context, *contribPubsub.PublishRequest) error
	bulkPublishFn func(context.Context, *contribPubsub.BulkPublishRequest) (contribPubsub.BulkPublishResponse, error)
}

func New() *Fake {
	return &Fake{
		publishFn: func(context.Context, *contribPubsub.PublishRequest) error {
			return nil
		},
		bulkPublishFn: func(context.Context, *contribPubsub.BulkPublishRequest) (contribPubsub.BulkPublishResponse, error) {
			return contribPubsub.BulkPublishResponse{}, nil
		},
	}
}

func (f *Fake) WithPublishFn(fn func(context.Context, *contribPubsub.PublishRequest) error) *Fake {
	f.publishFn = fn
	return f
}

func (f *Fake) WithBulkPublishFn(fn func(context.Context, *contribPubsub.BulkPublishRequest) (contribPubsub.BulkPublishResponse, error)) *Fake {
	f.bulkPublishFn = fn
	return f
}

func (f *Fake) Publish(ctx context.Context, req *contribPubsub.PublishRequest) error {
	return f.publishFn(ctx, req)
}

func (f *Fake) BulkPublish(ctx context.Context, req *contribPubsub.BulkPublishRequest) (contribPubsub.BulkPublishResponse, error) {
	return f.bulkPublishFn(ctx, req)
}
