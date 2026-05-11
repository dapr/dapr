/*
Copyright 2026 The Dapr Authors
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

package processor

import (
	"context"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
)

// AddPendingSubscription enqueues one or more declarative subscriptions.
// Returns a chan that emits one error per submitted subscription, in order.
func (p *Processor) AddPendingSubscription(ctx context.Context, subscriptions ...subapi.Subscription) <-chan error {
	if p.closed.Load() {
		return nil
	}
	res := make(chan error, len(subscriptions))
	for _, sub := range subscriptions {
		p.rootLoop.Loop().Enqueue(&loops.SubscriptionAdd{
			Subscription: sub,
			Result:       res,
		})
	}
	return res
}

// CloseSubscription closes a declarative subscription synchronously.
func (p *Processor) CloseSubscription(ctx context.Context, sub *subapi.Subscription) error {
	if p.closed.Load() {
		return nil
	}
	res := make(chan error, 1)
	p.rootLoop.Loop().Enqueue(&loops.SubscriptionClose{
		Subscription: *sub,
		Result:       res,
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-res:
		return err
	}
}
