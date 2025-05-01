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

package postman

import (
	"context"

	"github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman"
)

type Fake struct {
	deliverFn     func(context.Context, *pubsub.SubscribedMessage) error
	deliverBulkFn func(context.Context, *postman.DelivererBulkRequest) error
}

func New() *Fake {
	return &Fake{
		deliverFn: func(ctx context.Context, msg *pubsub.SubscribedMessage) error {
			return nil
		},
		deliverBulkFn: func(ctx context.Context, req *postman.DelivererBulkRequest) error {
			return nil
		},
	}
}

func (f *Fake) WithDeliverFn(fn func(context.Context, *pubsub.SubscribedMessage) error) *Fake {
	f.deliverFn = fn
	return f
}

func (f *Fake) WithDeliverBulkFn(fn func(context.Context, *postman.DelivererBulkRequest) error) *Fake {
	f.deliverBulkFn = fn
	return f
}

func (f *Fake) Deliver(ctx context.Context, msg *pubsub.SubscribedMessage) error {
	return f.deliverFn(ctx, msg)
}

func (f *Fake) DeliverBulk(ctx context.Context, req *postman.DelivererBulkRequest) error {
	return f.deliverBulkFn(ctx, req)
}
