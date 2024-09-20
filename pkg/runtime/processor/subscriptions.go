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
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/utils"
)

func (p *Processor) AddPendingSubscription(ctx context.Context, subscriptions ...subapi.Subscription) bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.shutdown.Load() {
		return false
	}

	scopedSubs := p.scopeFilterSubscriptions(subscriptions)
	if len(scopedSubs) == 0 {
		return true
	}

	for i := range scopedSubs {
		comp := scopedSubs[i]
		sub := rtpubsub.Subscription{
			PubsubName:      comp.Spec.Pubsubname,
			Topic:           comp.Spec.Topic,
			DeadLetterTopic: comp.Spec.DeadLetterTopic,
			Metadata:        comp.Spec.Metadata,
			Scopes:          comp.Scopes,
			BulkSubscribe: &rtpubsub.BulkSubscribe{
				Enabled:            comp.Spec.BulkSubscribe.Enabled,
				MaxMessagesCount:   comp.Spec.BulkSubscribe.MaxMessagesCount,
				MaxAwaitDurationMs: comp.Spec.BulkSubscribe.MaxAwaitDurationMs,
			},
		}
		for _, rule := range comp.Spec.Routes.Rules {
			erule, err := rtpubsub.CreateRoutingRule(rule.Match, rule.Path)
			if err != nil {
				p.errorSubscriptions(ctx, err)
				return false
			}
			sub.Rules = append(sub.Rules, erule)
		}
		if len(comp.Spec.Routes.Default) > 0 {
			sub.Rules = append(sub.Rules, &rtpubsub.Rule{
				Path: comp.Spec.Routes.Default,
			})
		}

		p.compStore.AddDeclarativeSubscription(&comp, sub)
		if err := p.subscriber.ReloadDeclaredAppSubscription(comp.Name, comp.Spec.Pubsubname); err != nil {
			p.compStore.DeleteDeclarativeSubscription(comp.Name)
			p.errorSubscriptions(ctx, err)
			return false
		}
	}

	return true
}

func (p *Processor) scopeFilterSubscriptions(subs []subapi.Subscription) []subapi.Subscription {
	scopedSubs := make([]subapi.Subscription, 0, len(subs))
	for _, sub := range subs {
		if len(sub.Scopes) > 0 && !utils.Contains[string](sub.Scopes, p.appID) {
			continue
		}
		scopedSubs = append(scopedSubs, sub)
	}
	return scopedSubs
}

func (p *Processor) CloseSubscription(ctx context.Context, sub *subapi.Subscription) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	if _, ok := p.compStore.GetDeclarativeSubscription(sub.Name); !ok {
		return nil
	}
	p.compStore.DeleteDeclarativeSubscription(sub.Name)
	if err := p.subscriber.ReloadDeclaredAppSubscription(sub.Name, sub.Spec.Pubsubname); err != nil {
		return err
	}
	return nil
}

func (p *Processor) processSubscriptions(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case <-p.closedCh:
		return nil
	case err := <-p.subErrCh:
		return err
	}
}

func (p *Processor) errorSubscriptions(ctx context.Context, err error) {
	select {
	case p.subErrCh <- err:
	case <-ctx.Done():
	case <-p.closedCh:
	}
}
