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

package compstore

import (
	"sync"

	"github.com/microsoft/durabletask-go/backend"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/components-contrib/crypto"
	"github.com/dapr/components-contrib/lock"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/workflows"
	compsv1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpEndpointV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

// ComponentStore is a store of all components which have been configured for the
// runtime. The store is dynamic. Each component type is indexed by its
// Component name.
type ComponentStore struct {
	lock sync.RWMutex

	states                  map[string]state.Store
	configurations          map[string]configuration.Store
	configurationSubscribes map[string]chan struct{}
	secretsConfigurations   map[string]config.SecretsScope
	secrets                 map[string]secretstores.SecretStore
	inputBindings           map[string]bindings.InputBinding
	inputBindingRoutes      map[string]string
	outputBindings          map[string]bindings.OutputBinding
	locks                   map[string]lock.Store
	pubSubs                 map[string]PubsubItem
	topicRoutes             map[string]TopicRoutes
	workflowComponents      map[string]workflows.Workflow
	workflowBackends        map[string]backend.Backend
	cryptoProviders         map[string]crypto.SubtleCrypto
	components              []compsv1alpha1.Component
	subscriptions           []rtpubsub.Subscription
	httpEndpoints           []httpEndpointV1alpha1.HTTPEndpoint
	actorStateStore         struct {
		name  string
		store state.Store
	}

	compPendingLock sync.Mutex
	compPending     *compsv1alpha1.Component
}

func New() *ComponentStore {
	return &ComponentStore{
		states:                  make(map[string]state.Store),
		configurations:          make(map[string]configuration.Store),
		configurationSubscribes: make(map[string]chan struct{}),
		secretsConfigurations:   make(map[string]config.SecretsScope),
		secrets:                 make(map[string]secretstores.SecretStore),
		inputBindings:           make(map[string]bindings.InputBinding),
		inputBindingRoutes:      make(map[string]string),
		outputBindings:          make(map[string]bindings.OutputBinding),
		locks:                   make(map[string]lock.Store),
		pubSubs:                 make(map[string]PubsubItem),
		workflowComponents:      make(map[string]workflows.Workflow),
		workflowBackends:        make(map[string]backend.Backend),
		cryptoProviders:         make(map[string]crypto.SubtleCrypto),
		topicRoutes:             make(map[string]TopicRoutes),
	}
}
