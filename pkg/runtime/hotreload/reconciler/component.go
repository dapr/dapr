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

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
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
	loader.Loader[compapi.Component]
}

func (c *component) update(ctx context.Context, comp compapi.Component) {
	oldComp, exists := c.store.GetComponent(comp.Spec.Type, comp.Name)
	_, _ = c.proc.Secret().ProcessResource(ctx, comp)

	if exists {
		if differ.AreSame(oldComp, comp) {
			log.Infof("Component update skipped: no changes detected: %s", comp.LogName())
			return
		}

		log.Infof("Closing existing Component to reload: %s", comp.LogName())
		// TODO: change close to accept pointer
		if err := c.proc.Close(comp); err != nil {
			log.Errorf("Error closing component: %s", err)
			return
		}
	}

	if !c.auth.IsObjectAuthorized(comp) {
		log.Warnf("Received unauthorized component update, ignored. %s", comp.LogName())
	}

	log.Infof("Adding Component for processing: %s", comp.LogName())
	c.proc.AddPendingComponent(ctx, comp)
}

func (c *component) delete(comp compapi.Component) {
	if err := c.proc.Close(comp); err != nil {
		log.Errorf("Error closing deleted component: %s", err)
	}
}
