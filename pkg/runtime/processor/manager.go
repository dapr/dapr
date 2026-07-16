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
	"strings"

	"github.com/dapr/durabletask-go/backend"

	"github.com/dapr/components-contrib/bindings"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/runtime/meta"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

// StateManager exposes the state sub-processor's read API. Lifecycle Init and
// Close are driven by the state category loop.
type StateManager interface {
	ActorStateStoreName() (string, bool)
}

// SecretManager exposes the secret sub-processor's ProcessResource read API
// (used by reconciler for resolving component references). Init and Close are
// driven by the secret category loop.
type SecretManager interface {
	// TODO: update to return an error.
	ProcessResource(context.Context, meta.Resource) (bool, string)
}

// SubscribeManager exposes the subscriber-facing operations. Each method
// remains synchronous; the underlying subscriber holds an internal mutex that
// guards against concurrent access from the pubsub category loop and from
// external callers (e.g. gRPC subscription stream handlers).
type SubscribeManager interface {
	InitProgramaticSubscriptions(context.Context) error
	StartAppSubscriptions() error
	StopAppSubscriptions()
	StopAllSubscriptionsForever()
	ReloadDeclaredAppSubscription(name, pubsubName string) error
	StartStreamerSubscription(sub *subapi.Subscription, connectionID rtpubsub.ConnectionID) error
	StopStreamerSubscription(sub *subapi.Subscription, connectionID rtpubsub.ConnectionID)
	ReloadPubSub(string) error
	StopPubSub(string)
}

// BindingManager exposes the binding sub-processor's read paths
// (SendToOutputBinding) and category-wide operations
// (StartReadingFromBindings, StopReadingFromBindings). Init and Close are
// driven by the bindings category loop.
type BindingManager interface {
	SendToOutputBinding(context.Context, string, *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	StartReadingFromBindings(context.Context) error
	StopReadingFromBindings(forever bool)
}

type WorkflowBackendManager interface {
	Backend() (backend.Backend, bool)
}

func (p *Processor) State() StateManager   { return p.state }
func (p *Processor) Secret() SecretManager { return p.secret }
func (p *Processor) Binding() BindingManager {
	return p.binding
}
func (p *Processor) Subscriber() SubscribeManager            { return p.subscriber }
func (p *Processor) WorkflowBackend() WorkflowBackendManager { return p.workflowBackend }

// category returns the components.Category that a component belongs to, based
// on its Spec.Type prefix. Returns the empty string if the prefix is not
// recognised.
//
// Exposed (unexported) for use by processor_test.go's TestExtractComponentCategory.
func (p *Processor) category(comp componentsapi.Component) components.Category {
	for _, cat := range []components.Category{
		components.CategoryBindings,
		components.CategoryConfiguration,
		components.CategoryCryptoProvider,
		components.CategoryLock,
		components.CategoryMiddleware,
		components.CategoryPubSub,
		components.CategorySecretStore,
		components.CategoryStateStore,
		components.CategoryConversation,
	} {
		if strings.HasPrefix(comp.Spec.Type, string(cat)+".") {
			return cat
		}
	}
	return ""
}
