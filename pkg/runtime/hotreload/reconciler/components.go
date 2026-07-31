/*
Copyright 2023 The Dapr Authors
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
	"fmt"
	"strings"

	"k8s.io/utils/clock"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/processor/state"
)

type components struct {
	store *compstore.ComponentStore
	proc  *processor.Processor
	auth  *authorizer.Authorizer
	loader.Loader[compapi.Component]
}

func NewComponents(opts Options[compapi.Component]) *Reconciler[compapi.Component] {
	r := &Reconciler[compapi.Component]{
		kind:     compapi.Kind,
		htarget:  opts.Healthz.AddTarget("component-reconciler"),
		interval: opts.ReconcileInterval,
		clock:    clock.RealClock{},
		manager: &components{
			Loader: opts.Loader.Components(),
			store:  opts.CompStore,
			proc:   opts.Processor,
			auth:   opts.Authorizer,
		},
	}
	r.loop = loopFactory.NewLoop(r)
	return r
}

func (c *components) update(ctx context.Context, comp compapi.Component) error {
	// Only a single actor state store may be configured. Skip a component
	// which would become a second actor state store, rather than failing its
	// init and exiting daprd.
	if _, name, ok := c.store.GetStateStoreActor(); ok && name != comp.Name && isMarkedActorStateStore(comp) {
		log.Errorf("Aborting hot reload of %s: %s is already the actor state store, only one actor state store is allowed", comp.LogName(), name)
		return nil
	}

	// Notify the actor runtime once the update fully settles, and only if
	// the actor state store actually changed. Notifying after both the close
	// and re-init of an updated component means the actor runtime never
	// observes the transient store-less state in the middle of an update.
	defer c.notifyActorStateStoreChanged()()

	oldComp, exists := c.store.GetComponent(comp.Name)
	_, _ = c.proc.Secret().ProcessResource(ctx, comp)

	if exists {
		if differ.AreSame(oldComp, comp) {
			log.Debugf("Component update skipped: no changes detected: %s", comp.LogName())
			return nil
		}

		// Reject events that carry a lower Generation than what is installed.
		// In Kubernetes, Generation is monotonically increased by the API
		// server on each spec change; in self-hosted mode the disk loader
		// stamps a process-wide monotonic counter. Lower generation reliably
		// means an older event arrived out of order; skip rather than
		// downgrade the installed version.
		if comp.GetGeneration() > 0 && comp.GetGeneration() < oldComp.GetGeneration() {
			log.Warnf("Ignoring stale Component event for %s (generation %d < installed %d)",
				comp.LogName(), comp.GetGeneration(), oldComp.GetGeneration())
			return nil
		}

		log.Infof("Closing existing Component to reload: %s", oldComp.LogName())
		// TODO: change close to accept pointer
		if err := c.proc.Close(ctx, oldComp); err != nil {
			log.Errorf("error closing old component: %s", err)
			return nil
		}
	}

	if !c.auth.IsObjectAuthorized(comp) {
		log.Warnf("Received unauthorized component update, ignored: %s", comp.LogName())
		return nil
	}

	log.Infof("Adding Component for processing: %s", comp.LogName())

	res := c.proc.AddPendingComponent(ctx, comp)
	if res == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-res:
		if err == nil {
			log.Infof("Component updated: %s", comp.LogName())
			return nil
		}
		err = fmt.Errorf("process component %s error: %s", comp.Name, err)
		if comp.Spec.IgnoreErrors {
			log.Errorf("Ignoring error processing component: %s", err)
			return nil
		}
		log.Warnf("Error processing component, daprd will exit gracefully: %s", err)
		return err
	}
}

func (c *components) delete(ctx context.Context, comp compapi.Component) error {
	defer c.notifyActorStateStoreChanged()()

	if err := c.proc.Close(ctx, comp); err != nil {
		log.Errorf("error closing deleted component: %s", err)
	}
	return nil
}

// notifyActorStateStoreChanged captures the actor state store revision and
// returns a func which notifies the actor runtime if the revision has since
// changed.
func (c *components) notifyActorStateStoreChanged() func() {
	//nolint:dogsled
	_, _, before, _ := c.store.GetStateStoreActorWithRevision()
	return func() {
		if _, _, after, _ := c.store.GetStateStoreActorWithRevision(); after != before {
			c.proc.OnActorStateStoreChanged()
		}
	}
}

// isMarkedActorStateStore returns whether the component is a state store
// carrying the actor state store metadata key. Presence based, since the
// value may be resolved from a secret.
func isMarkedActorStateStore(comp compapi.Component) bool {
	if !strings.HasPrefix(comp.Spec.Type, "state.") {
		return false
	}
	for _, meta := range comp.Spec.Metadata {
		if strings.EqualFold(meta.Name, state.PropertyKeyActorStateStore) {
			return true
		}
	}
	return false
}
