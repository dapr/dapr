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

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/processor/state"
)

type component struct {
	store *compstore.ComponentStore
	proc  *processor.Processor
	auth  *authorizer.Authorizer
	loader.Loader[componentsapi.Component]
}

// The go linter does not yet understand that these functions are being used by
// the generic reconciler.
//
//nolint:unused
func (c *component) update(ctx context.Context, comp componentsapi.Component) {
	if !c.verify(comp) {
		return
	}

	oldComp, exists := c.store.GetComponent(comp.Name)
	_, _ = c.proc.Secret().ProcessResource(ctx, comp)

	if exists {
		if differ.AreSame(oldComp, comp) {
			log.Debugf("Component update skipped: no changes detected: %s", comp.LogName())
			return
		}

		log.Infof("Closing existing Component to reload: %s", oldComp.LogName())
		// TODO: change close to accept pointer
		if err := c.proc.Close(oldComp); err != nil {
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
	}

	return
}

//nolint:unused
func (c *component) delete(comp componentsapi.Component) {
	if !c.verify(comp) {
		return
	}

	if err := c.proc.Close(comp); err != nil {
		log.Errorf("error closing deleted component: %s", err)
	}
}

//nolint:unused
func (c *component) verify(vcomp componentsapi.Component) bool {
	toverify := []componentsapi.Component{vcomp}
	if comp, ok := c.store.GetComponent(vcomp.Name); ok {
		toverify = append(toverify, comp)
	}

	// Reject component if it has the same name as the actor state store.
	if _, name, ok := c.store.GetStateStoreActor(); ok && name == vcomp.Name {
		log.Errorf("Aborting to hot-reload a state store component that is used as an actor state store: %s", vcomp.LogName())
		return false
	}

	// Reject component if it is being marked as the actor state store.
	for _, comp := range toverify {
		if strings.HasPrefix(comp.Spec.Type, "state.") {
			for _, meta := range comp.Spec.Metadata {
				if strings.EqualFold(meta.Name, state.PropertyKeyActorStateStore) {
					log.Errorf("Aborting to hot-reload a state store component that is used as an actor state store: %s", comp.LogName())
					return false
				}
			}
		}
	}

	for backendName := range c.store.ListWorkflowBackends() {
		if backendName == vcomp.Name {
			log.Errorf("Aborting to hot-reload a workflowbackend component which is not supported: %s", vcomp.LogName())
			return false
		}
	}

	if strings.HasPrefix(vcomp.Spec.Type, "workflowbackend.") {
		log.Errorf("Aborting to hot-reload a workflowbackend component which is not supported: %s", vcomp.LogName())
		return false
	}

	return true
}
