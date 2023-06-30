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
	"sync"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor/binding"
	"github.com/dapr/dapr/pkg/runtime/processor/configuration"
	"github.com/dapr/dapr/pkg/runtime/processor/crypto"
	"github.com/dapr/dapr/pkg/runtime/processor/lock"
	"github.com/dapr/dapr/pkg/runtime/processor/middleware"
	"github.com/dapr/dapr/pkg/runtime/processor/pubsub"
	"github.com/dapr/dapr/pkg/runtime/processor/secret"
	"github.com/dapr/dapr/pkg/runtime/processor/state"
	"github.com/dapr/dapr/pkg/runtime/processor/workflow"
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

// manager implements the life cycle events of a component category.
type manager interface {
	Init(context.Context, compapi.Component) error
	Close(compapi.Component) error
}

type stateManager interface {
	ActorStateStoreName() (string, bool)
	manager
}

// Processor manages the lifecycle of all components categories.
type Processor struct {
	compStore *compstore.ComponentStore
	managers  map[components.Category]manager
	state     stateManager

	lock sync.RWMutex
}

func New(opts Options) *Processor {
	pubsub := pubsub.New(pubsub.Options{
		ID:             opts.ID,
		Registry:       opts.Registry.PubSubs(),
		ComponentStore: opts.ComponentStore,
		Meta:           opts.Meta,
	})

	state := state.New(state.Options{
		PlacementEnabled: opts.PlacementEnabled,
		Registry:         opts.Registry.StateStores(),
		ComponentStore:   opts.ComponentStore,
		Meta:             opts.Meta,
	})

	return &Processor{
		compStore: opts.ComponentStore,
		state:     state,
		managers: map[components.Category]manager{
			components.CategoryBindings: binding.New(binding.Options{
				Registry:       opts.Registry.Bindings(),
				ComponentStore: opts.ComponentStore,
				Meta:           opts.Meta,
			}),
			components.CategoryConfiguration: configuration.New(configuration.Options{
				Registry:       opts.Registry.Configurations(),
				ComponentStore: opts.ComponentStore,
				Meta:           opts.Meta,
			}),
			components.CategoryCryptoProvider: crypto.New(crypto.Options{
				Registry:       opts.Registry.Crypto(),
				ComponentStore: opts.ComponentStore,
				Meta:           opts.Meta,
			}),
			components.CategoryLock: lock.New(lock.Options{
				Registry:       opts.Registry.Locks(),
				ComponentStore: opts.ComponentStore,
				Meta:           opts.Meta,
			}),
			components.CategoryPubSub: pubsub,
			components.CategorySecretStore: secret.New(secret.Options{
				Registry:       opts.Registry.SecretStores(),
				ComponentStore: opts.ComponentStore,
				Meta:           opts.Meta,
			}),
			components.CategoryStateStore: state,
			components.CategoryWorkflow: workflow.New(workflow.Options{
				Registry:       opts.Registry.Workflows(),
				ComponentStore: opts.ComponentStore,
				Meta:           opts.Meta,
			}),
			components.CategoryMiddleware: middleware.New(),
		},
	}
}

// Init initializes a component of a category.
func (p *Processor) Init(ctx context.Context, comp compapi.Component) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	m, err := p.managerFromComp(comp)
	if err != nil {
		return err
	}

	return m.Init(ctx, comp)
}

// Close closes the component.
func (p *Processor) Close(comp compapi.Component) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	m, err := p.managerFromComp(comp)
	if err != nil {
		return err
	}

	if err := m.Close(comp); err != nil {
		return err
	}

	p.compStore.DeleteComponent(comp.Spec.Type, comp.Name)

	return nil
}

func (p *Processor) managerFromComp(comp compapi.Component) (manager, error) {
	category := p.Category(comp)
	m, ok := p.managers[category]
	if !ok {
		return nil, fmt.Errorf("unknown component category: %q", category)
	}
	return m, nil
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
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.state.ActorStateStoreName()
}
