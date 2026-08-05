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
	"strings"
	"sync"

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

	// skippedActorStore stashes the most recent component skipped because
	// another component currently occupies the actor state store slot. It is
	// replayed once that store is removed or unmarked, healing an
	// out-of-order create-before-delete rename delivered by an event stream.
	skippedActorStore     *compapi.Component
	skippedActorStoreLock sync.Mutex
}

// The go linter does not yet understand that these functions are being used by
// the generic reconciler.
//
//nolint:unused
func (c *components) update(ctx context.Context, comp compapi.Component) {
	// Only a single actor state store may be configured. Skip a component
	// which would become a second actor state store, rather than failing its
	// init and exiting daprd. The skipped component is stashed and replayed
	// if the current actor state store is removed, so a rename delivered as
	// create-before-delete converges without waiting for the next reconcile.
	if _, name, ok := c.store.GetStateStoreActor(); ok && name != comp.Name && isMarkedActorStateStore(comp) {
		log.Errorf("Skipping hot reload of %s: %s is already the actor state store, only one actor state store is allowed. The component will be applied if %s is removed", comp.LogName(), name, name)
		c.skippedActorStoreLock.Lock()
		c.skippedActorStore = &comp
		c.skippedActorStoreLock.Unlock()
		return
	}

	// Any other event for the stashed component supersedes the stash.
	c.dropSkippedActorStore(comp.Name)

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
			return
		}

		log.Infof("Closing existing Component to reload: %s", oldComp.LogName())
		// TODO: change close to accept pointer
		err := c.proc.Close(oldComp)
		if err != nil {
			log.Errorf("error closing old component: %s", err)
			return
		}
	}

	if !c.auth.IsObjectAuthorized(comp) {
		log.Warnf("Received unauthorized component update, ignored: %s", comp.LogName())
		return
	}

	log.Infof("Adding Component for processing: %s", comp.LogName())

	if c.proc.AddPendingComponent(ctx, comp) {
		log.Infof("Component updated: %s", comp.LogName())
		c.proc.WaitForEmptyComponentQueue()
		// An update which unmarked the actor state store frees the slot for a
		// previously skipped component.
		c.replaySkippedActorStore(ctx)
	}
}

//nolint:unused
func (c *components) delete(ctx context.Context, comp compapi.Component) {
	c.dropSkippedActorStore(comp.Name)

	defer c.notifyActorStateStoreChanged()()

	err := c.proc.Close(comp)
	if err != nil {
		log.Errorf("error closing deleted component: %s", err)
	}

	c.replaySkippedActorStore(ctx)
}

// dropSkippedActorStore forgets the stashed skipped actor state store when a
// newer event for the same component arrives.
//
//nolint:unused
func (c *components) dropSkippedActorStore(name string) {
	c.skippedActorStoreLock.Lock()
	defer c.skippedActorStoreLock.Unlock()
	if c.skippedActorStore != nil && c.skippedActorStore.Name == name {
		c.skippedActorStore = nil
	}
}

// replaySkippedActorStore applies the stashed skipped actor state store if
// the actor state store slot has become free.
//
//nolint:unused
func (c *components) replaySkippedActorStore(ctx context.Context) {
	c.skippedActorStoreLock.Lock()
	skipped := c.skippedActorStore
	if skipped == nil {
		c.skippedActorStoreLock.Unlock()
		return
	}
	if _, _, ok := c.store.GetStateStoreActor(); ok {
		c.skippedActorStoreLock.Unlock()
		return
	}
	c.skippedActorStore = nil
	c.skippedActorStoreLock.Unlock()

	log.Infof("Applying previously skipped actor state store: %s", skipped.LogName())
	c.update(ctx, *skipped)
}

// notifyActorStateStoreChanged captures the actor state store revision and
// returns a func which notifies the actor runtime if the revision has since
// changed.
//
//nolint:unused
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
//
//nolint:unused
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
