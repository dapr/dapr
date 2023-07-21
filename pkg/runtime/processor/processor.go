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

package processor

import (
	"context"
	"fmt"
	"strings"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.processor")

type Options struct {
	// ID is the ID of this Dapr instance.
	ID string

	// PlacementEnabled indicates whether placement service is enabled in this
	// Dapr cluster.
	PlacementEnabled bool

	// Registry is the all-component registry.
	Registry *registry.Registry

	// ComponentStore is the component store.
	ComponentStore *compstore.ComponentStore

	// Metadata is the metadata helper.
	Meta *meta.Meta
}

// Processor manages the lifecycle of all components categories.
type Processor struct {
	managers map[components.Category]manager
}

// manager implements the life cycle events of a component category.
type manager interface {
	init(context.Context, compapi.Component) error
}

func New(opts Options) *Processor {
	return &Processor{
		managers: map[components.Category]manager{
			components.CategoryBindings: &binding{
				registry:  opts.Registry.Bindings(),
				compStore: opts.ComponentStore,
				meta:      opts.Meta,
			},
			components.CategoryConfiguration: &configuration{
				registry:  opts.Registry.Configurations(),
				compStore: opts.ComponentStore,
				meta:      opts.Meta,
			},
			components.CategoryCryptoProvider: &crypto{
				registry:  opts.Registry.Crypto(),
				compStore: opts.ComponentStore,
				meta:      opts.Meta,
			},
			components.CategoryLock: &lock{
				registry:  opts.Registry.Locks(),
				compStore: opts.ComponentStore,
				meta:      opts.Meta,
			},
			components.CategoryPubSub: &pubsub{
				id:        opts.ID,
				registry:  opts.Registry.PubSubs(),
				compStore: opts.ComponentStore,
				meta:      opts.Meta,
			},
			components.CategorySecretStore: &secret{
				registry:  opts.Registry.SecretStores(),
				compStore: opts.ComponentStore,
				meta:      opts.Meta,
			},
			components.CategoryStateStore: &state{
				placementEnabled: opts.PlacementEnabled,
				registry:         opts.Registry.StateStores(),
				compStore:        opts.ComponentStore,
				meta:             opts.Meta,
			},
			components.CategoryWorkflow: &workflow{
				registry:  opts.Registry.Workflows(),
				compStore: opts.ComponentStore,
				meta:      opts.Meta,
			},
			components.CategoryMiddleware: &middleware{},
		},
	}
}

// One initializes a component of a category.
func (p *Processor) One(ctx context.Context, comp compapi.Component) error {
	category := p.Category(comp)
	m, ok := p.managers[category]
	if !ok {
		return fmt.Errorf("unknown component category: %q", category)
	}

	return m.init(ctx, comp)
}

func (p *Processor) Category(comp compapi.Component) components.Category {
	for category := range p.managers {
		if strings.HasPrefix(comp.Spec.Type, string(category)+".") {
			return category
		}
	}
	return ""
}

func (p *Processor) ActorStateStore() (string, bool) {
	state, ok := p.managers[components.CategoryStateStore].(*state)
	if !ok {
		return "", false
	}
	state.lock.Lock()
	defer state.lock.Unlock()
	return state.actorStateStoreName, len(state.actorStateStoreName) > 0
}
