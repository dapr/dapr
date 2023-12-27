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
	if strings.HasPrefix(comp.Spec.Type, "middleware.") {
		log.Warnf("Hotreload is not supported for middleware components: %s", comp.LogName())
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
			log.Errorf("error closing old component: %w", err)
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
}

//nolint:unused
func (c *component) delete(comp componentsapi.Component) {
	if err := c.proc.Close(comp); err != nil {
		log.Errorf("error closing deleted component: %w", err)
	}
}
