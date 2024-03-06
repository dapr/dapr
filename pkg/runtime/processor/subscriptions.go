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

package processor

import (
	"context"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/utils"
)

func (p *Processor) AddPendingSubscription(ctx context.Context, subscription subapi.Subscription) bool {
	p.chlock.RLock()
	defer p.chlock.RUnlock()
	if p.shutdown.Load() {
		return false
	}

	select {
	case <-ctx.Done():
		return false
	case <-p.closedCh:
		return false
	case p.pendingSubscriptions <- subscription:
		return true
	}
}

func (p *Processor) CloseSubscription(ctx context.Context, sub subapi.Subscription) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	if _, ok := p.compStore.GetDeclarativeSubscription(sub.Name); !ok {
		return nil
	}
	p.compStore.DeleteDeclaraiveSubscription(sub.Name)
	if err := p.pubsub.ReloadSubscriptions(ctx); err != nil {
		return err
	}
	return nil
}

func (p *Processor) processSubscriptions(ctx context.Context) error {
	for subscription := range p.pendingSubscriptions {
		if len(subscription.Name) == 0 {
			continue
		}
		if len(subscription.Scopes) > 0 && !utils.Contains[string](subscription.Scopes, p.appID) {
			continue
		}
		if err := p.compStore.AddDeclarativeSubscription(subscription); err != nil {
			return err
		}
		if err := p.PubSub().ReloadSubscriptions(ctx); err != nil {
			return err
		}
	}

	return nil
}
