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

package runtime

import (
	"context"
	"slices"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/internal/loader"
	"github.com/dapr/dapr/pkg/internal/loader/disk"
	"github.com/dapr/dapr/pkg/internal/loader/kubernetes"
	"github.com/dapr/dapr/pkg/modes"
)

func (a *DaprRuntime) loadDeclarativeSubscriptions(ctx context.Context) error {
	var l loader.Loader[subapi.Subscription]

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		l = kubernetes.NewSubscriptions(kubernetes.Options{
			Client:    a.operatorClient,
			Namespace: a.namespace,
		})
	case modes.StandaloneMode:
		l = disk.NewSubscriptions(disk.Options{
			AppID: a.runtimeConfig.id,
			Paths: a.runtimeConfig.standalone.ResourcesPath,
		})
	default:
		return nil
	}

	log.Info("Loading Declarative Subscriptions…")

	subs, err := l.Load(ctx)
	if err != nil {
		return err
	}

	for _, s := range subs {
		if subscriptionIsInScope(a.runtimeConfig.id, s) {
			log.Infof("Found Subscription: %s", s.Name)
		}
	}

	// Wait for every declarative subscription to be committed to the component
	// store before returning. Declarative subscriptions are deduplicated per
	// topic with first-declared priority when the subscriber starts, so they
	// must all be present (and in manifest order) before StartAppSubscriptions
	// runs later in init. Returning early would let app subscriptions start
	// against a partial set and race in late additions out of order.
	ch := a.processor.AddPendingSubscription(ctx, subs...)
	if ch == nil {
		return nil
	}
	for range subs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-ch:
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func subscriptionIsInScope(appID string, subscription subapi.Subscription) bool {
	return len(subscription.Scopes) == 0 || slices.Contains(subscription.Scopes, appID)
}
