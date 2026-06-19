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

package reconciler

import (
	"context"

	"k8s.io/utils/clock"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/processor"
)

type subscriptions struct {
	store *compstore.ComponentStore
	proc  *processor.Processor
	loader.Loader[subapi.Subscription]
}

func NewSubscriptions(opts Options[subapi.Subscription]) *Reconciler[subapi.Subscription] {
	r := &Reconciler[subapi.Subscription]{
		kind:     subapi.Kind,
		htarget:  opts.Healthz.AddTarget("subscription-reconciler"),
		interval: opts.ReconcileInterval,
		clock:    clock.RealClock{},
		manager: &subscriptions{
			Loader: opts.Loader.Subscriptions(),
			store:  opts.CompStore,
			proc:   opts.Processor,
		},
	}
	r.loop = loopFactory.NewLoop(r)
	return r
}

// The go linter does not yet understand that these functions are being used by
// the generic reconciler.
//
//nolint:unused
func (s *subscriptions) update(ctx context.Context, sub subapi.Subscription) error {
	oldSub, exists := s.store.GetDeclarativeSubscription(sub.Name)

	if exists {
		if differ.AreSame(*oldSub.Comp, sub) {
			log.Debugf("Subscription update skipped: no changes detected: %s", sub.Name)
			return nil
		}

		// See components.update for the rationale on this guard.
		if sub.GetGeneration() > 0 && sub.GetGeneration() < oldSub.Comp.GetGeneration() {
			log.Warnf("Ignoring stale Subscription event for %s (generation %d < installed %d)",
				sub.Name, sub.GetGeneration(), oldSub.Comp.GetGeneration())
			return nil
		}

		log.Infof("Closing existing Subscription to reload: %s", *oldSub.Name)

		if err := s.proc.CloseSubscription(ctx, oldSub.Comp); err != nil {
			log.Errorf("Failed to close existing Subscription: %s", err)
			return nil
		}
	}

	log.Infof("Adding Subscription for processing: %s", sub.Name)

	res := s.proc.AddPendingSubscription(ctx, sub)
	if res == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-res:
		if err != nil {
			// Subscriptions have no IgnoreErrors, so a materialisation failure
			// must propagate and stop daprd rather than leave the compstore and
			// subscriber partially updated. This matches the components
			// reconciler, which propagates its AddPendingComponent error.
			log.Warnf("Error adding subscription %s, daprd will exit gracefully: %s", sub.Name, err)
			return err
		}
		log.Infof("Subscription updated: %s", sub.Name)
		return nil
	}
}

//nolint:unused
func (s *subscriptions) delete(ctx context.Context, sub subapi.Subscription) error {
	if err := s.proc.CloseSubscription(ctx, &sub); err != nil {
		log.Errorf("Failed to close Subscription: %s", err)
	}
	return nil
}
