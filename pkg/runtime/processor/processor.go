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
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	apiextapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	grpcmanager "github.com/dapr/dapr/pkg/api/grpc/manager"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/config"
	configmodes "github.com/dapr/dapr/pkg/config/modes"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/internal/apis"
	"github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/modes"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor/binding"
	"github.com/dapr/dapr/pkg/runtime/processor/configuration"
	"github.com/dapr/dapr/pkg/runtime/processor/crypto"
	"github.com/dapr/dapr/pkg/runtime/processor/lock"
	"github.com/dapr/dapr/pkg/runtime/processor/middleware"
	"github.com/dapr/dapr/pkg/runtime/processor/pubsub"
	"github.com/dapr/dapr/pkg/runtime/processor/secret"
	"github.com/dapr/dapr/pkg/runtime/processor/state"
	"github.com/dapr/dapr/pkg/runtime/processor/wfbackend"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

const (
	defaultComponentInitTimeout = time.Second * 5
)

var log = logger.NewLogger("dapr.runtime.processor")

type Options struct {
	// ID is the ID of this Dapr instance.
	ID string

	// Namespace is the namespace of this Dapr instance.
	Namespace string

	// Mode is the mode of this Dapr instance.
	Mode modes.DaprMode

	// PodName is the name of the pod.
	PodName string

	// ActorsEnabled indicates whether placement service is enabled in this Dapr cluster.
	ActorsEnabled bool

	// IsHTTP indicates whether the connection to the application is using the
	// HTTP protocol.
	IsHTTP bool

	// Registry is the all-component registry.
	Registry *registry.Registry

	// ComponentStore is the component store.
	ComponentStore *compstore.ComponentStore

	// Metadata is the metadata helper.
	Meta *meta.Meta

	// GlobalConfig is the global configuration.
	GlobalConfig *config.Configuration

	Standalone configmodes.StandaloneConfig

	Resiliency resiliency.Provider

	GRPC *grpcmanager.Manager

	Channels *channels.Channels

	OperatorClient operatorv1.OperatorClient

	MiddlewareHTTP *http.HTTP
}

// Processor manages the lifecycle of all components categories.
type Processor struct {
	compStore       *compstore.ComponentStore
	managers        map[components.Category]manager
	state           StateManager
	secret          SecretManager
	pubsub          PubsubManager
	binding         BindingManager
	workflowBackend WorkflowBackendManager

	pendingHTTPEndpoints       chan httpendpointsapi.HTTPEndpoint
	pendingComponents          chan componentsapi.Component
	pendingComponentsWaiting   sync.WaitGroup
	pendingComponentDependents map[string][]componentsapi.Component

	lock     sync.RWMutex
	chlock   sync.RWMutex
	running  atomic.Bool
	shutdown atomic.Bool
	closedCh chan struct{}
}

func New(opts Options) *Processor {
	ps := pubsub.New(pubsub.Options{
		ID:             opts.ID,
		Namespace:      opts.Namespace,
		Mode:           opts.Mode,
		PodName:        opts.PodName,
		IsHTTP:         opts.IsHTTP,
		Registry:       opts.Registry.PubSubs(),
		ComponentStore: opts.ComponentStore,
		Meta:           opts.Meta,
		Resiliency:     opts.Resiliency,
		TracingSpec:    opts.GlobalConfig.Spec.TracingSpec,
		GRPC:           opts.GRPC,
		Channels:       opts.Channels,
		OperatorClient: opts.OperatorClient,
		ResourcesPath:  opts.Standalone.ResourcesPath,
	})

	state := state.New(state.Options{
		ActorsEnabled:  opts.ActorsEnabled,
		Registry:       opts.Registry.StateStores(),
		ComponentStore: opts.ComponentStore,
		Meta:           opts.Meta,
		Outbox:         ps.Outbox(),
	})

	secret := secret.New(secret.Options{
		Registry:       opts.Registry.SecretStores(),
		ComponentStore: opts.ComponentStore,
		Meta:           opts.Meta,
		OperatorClient: opts.OperatorClient,
	})

	binding := binding.New(binding.Options{
		Registry:       opts.Registry.Bindings(),
		ComponentStore: opts.ComponentStore,
		Meta:           opts.Meta,
		IsHTTP:         opts.IsHTTP,
		Resiliency:     opts.Resiliency,
		GRPC:           opts.GRPC,
		TracingSpec:    opts.GlobalConfig.Spec.TracingSpec,
		Channels:       opts.Channels,
	})

	wfbe := wfbackend.New(wfbackend.Options{
		AppID:          opts.ID,
		Registry:       opts.Registry.WorkflowBackends(),
		ComponentStore: opts.ComponentStore,
		Meta:           opts.Meta,
	})

	return &Processor{
		pendingHTTPEndpoints:       make(chan httpendpointsapi.HTTPEndpoint),
		pendingComponents:          make(chan componentsapi.Component),
		pendingComponentDependents: make(map[string][]componentsapi.Component),
		closedCh:                   make(chan struct{}),
		compStore:                  opts.ComponentStore,
		state:                      state,
		pubsub:                     ps,
		binding:                    binding,
		secret:                     secret,
		workflowBackend:            wfbe,
		managers: map[components.Category]manager{
			components.CategoryBindings: binding,
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
			components.CategoryPubSub:          ps,
			components.CategorySecretStore:     secret,
			components.CategoryStateStore:      state,
			components.CategoryWorkflowBackend: wfbe,
			components.CategoryMiddleware: middleware.New(middleware.Options{
				Meta:         opts.Meta,
				RegistryHTTP: opts.Registry.HTTPMiddlewares(),
				HTTP:         opts.MiddlewareHTTP,
			}),
		},
	}
}

// Init initializes a component of a category.
func (p *Processor) Init(ctx context.Context, comp componentsapi.Component) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	m, err := p.managerFromComp(comp)
	if err != nil {
		return err
	}

	if err := p.compStore.AddPendingComponentForCommit(comp); err != nil {
		return err
	}

	if err := m.Init(ctx, comp); err != nil {
		return errors.Join(err, p.compStore.DropPendingComponent())
	}

	return p.compStore.CommitPendingComponent()
}

// Close closes the component.
func (p *Processor) Close(comp componentsapi.Component) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	m, err := p.managerFromComp(comp)
	if err != nil {
		return err
	}

	if err := m.Close(comp); err != nil {
		return err
	}

	p.compStore.DeleteComponent(comp.Name)

	return nil
}

type componentPreprocessRes struct {
	unreadyDependency string
}

func (p *Processor) Process(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return errors.New("processor is already running")
	}

	return concurrency.NewRunnerManager(
		p.processComponents,
		p.processHTTPEndpoints,
		func(ctx context.Context) error {
			<-ctx.Done()
			close(p.closedCh)
			p.chlock.Lock()
			defer p.chlock.Unlock()
			p.shutdown.Store(true)
			close(p.pendingComponents)
			close(p.pendingHTTPEndpoints)
			return nil
		},
	).Run(ctx)
}

func (p *Processor) AddPendingComponent(ctx context.Context, comp componentsapi.Component) bool {
	p.chlock.RLock()
	defer p.chlock.RUnlock()

	if p.shutdown.Load() {
		return false
	}

	p.pendingComponentsWaiting.Add(1)
	select {
	case <-ctx.Done():
		p.pendingComponentsWaiting.Done()
		return false
	case <-p.closedCh:
		p.pendingComponentsWaiting.Done()
		return false
	case p.pendingComponents <- comp:
		return true
	}
}

func (p *Processor) AddPendingEndpoint(ctx context.Context, endpoint httpendpointsapi.HTTPEndpoint) bool {
	p.chlock.RLock()
	defer p.chlock.RUnlock()
	if p.shutdown.Load() {
		return false
	}

	select {
	case <-ctx.Done():
		return false
	case <-p.closedCh:
		return false
	case p.pendingHTTPEndpoints <- endpoint:
		return true
	}
}

func (p *Processor) processComponents(ctx context.Context) error {
	process := func(comp componentsapi.Component) error {
		if comp.Name == "" {
			return nil
		}

		err := p.processComponentAndDependents(ctx, comp)
		if err != nil {
			err = fmt.Errorf("process component %s error: %s", comp.Name, err)
			if !comp.Spec.IgnoreErrors {
				log.Warnf("Error processing component, daprd will exit gracefully")
				return err
			}
			log.Error(err)
		}
		return nil
	}

	for comp := range p.pendingComponents {
		err := process(comp)
		p.pendingComponentsWaiting.Done()
		if err != nil {
			return err
		}
	}

	return nil
}

func (p *Processor) processHTTPEndpoints(ctx context.Context) error {
	for endpoint := range p.pendingHTTPEndpoints {
		if endpoint.Name == "" {
			continue
		}
		p.processHTTPEndpointSecrets(ctx, &endpoint)
		p.compStore.AddHTTPEndpoint(endpoint)
	}

	return nil
}

// WaitForEmptyComponentQueue waits for the component queue to be empty.
func (p *Processor) WaitForEmptyComponentQueue() {
	p.pendingComponentsWaiting.Wait()
}

func (p *Processor) processComponentAndDependents(ctx context.Context, comp componentsapi.Component) error {
	log.Debug("Loading component: " + comp.LogName())
	res := p.preprocessOneComponent(ctx, &comp)
	if res.unreadyDependency != "" {
		p.pendingComponentDependents[res.unreadyDependency] = append(p.pendingComponentDependents[res.unreadyDependency], comp)
		return nil
	}

	compCategory := p.category(comp)
	if compCategory == "" {
		// the category entered is incorrect, return error
		return fmt.Errorf("incorrect type %s", comp.Spec.Type)
	}

	timeout, err := time.ParseDuration(comp.Spec.InitTimeout)
	if err != nil {
		timeout = defaultComponentInitTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err = p.Init(ctx, comp)
	// If the context is canceled, we want  to return an init error.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("init timeout for component %s exceeded after %s", comp.LogName(), timeout.String())
	}
	if err != nil {
		log.Errorf("Failed to init component %s: %s", comp.LogName(), err)
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, comp.LogName(), err)
	}

	log.Info("Component loaded: " + comp.LogName())
	diag.DefaultMonitoring.ComponentLoaded()

	dependency := componentDependency(compCategory, comp.Name)
	if deps, ok := p.pendingComponentDependents[dependency]; ok {
		delete(p.pendingComponentDependents, dependency)
		for _, dependent := range deps {
			if err := p.processComponentAndDependents(ctx, dependent); err != nil {
				return err
			}
		}
	}

	return nil
}

func (p *Processor) processHTTPEndpointSecrets(ctx context.Context, endpoint *httpendpointsapi.HTTPEndpoint) {
	_, _ = p.secret.ProcessResource(ctx, endpoint)

	tlsResource := apis.GenericNameValueResource{
		Name:        endpoint.ObjectMeta.Name,
		Namespace:   endpoint.ObjectMeta.Namespace,
		SecretStore: endpoint.Auth.SecretStore,
		Pairs:       []commonapi.NameValuePair{},
	}

	root, clientCert, clientKey := "root", "clientCert", "clientKey"

	ca := commonapi.NameValuePair{
		Name: root,
	}

	if endpoint.HasTLSRootCA() {
		ca.Value = *endpoint.Spec.ClientTLS.RootCA.Value
	}

	if endpoint.HasTLSRootCASecret() {
		ca.SecretKeyRef = *endpoint.Spec.ClientTLS.RootCA.SecretKeyRef
	}
	tlsResource.Pairs = append(tlsResource.Pairs, ca)

	cCert := commonapi.NameValuePair{
		Name: clientCert,
	}

	if endpoint.HasTLSClientCert() {
		cCert.Value = *endpoint.Spec.ClientTLS.Certificate.Value
	}

	if endpoint.HasTLSClientCertSecret() {
		cCert.SecretKeyRef = *endpoint.Spec.ClientTLS.Certificate.SecretKeyRef
	}
	tlsResource.Pairs = append(tlsResource.Pairs, cCert)

	cKey := commonapi.NameValuePair{
		Name: clientKey,
	}

	if endpoint.HasTLSPrivateKey() {
		cKey.Value = *endpoint.Spec.ClientTLS.PrivateKey.Value
	}

	if endpoint.HasTLSPrivateKeySecret() {
		cKey.SecretKeyRef = *endpoint.Spec.ClientTLS.PrivateKey.SecretKeyRef
	}

	tlsResource.Pairs = append(tlsResource.Pairs, cKey)

	updated, _ := p.secret.ProcessResource(ctx, tlsResource)
	if updated {
		for _, np := range tlsResource.Pairs {
			dv := &commonapi.DynamicValue{
				JSON: apiextapi.JSON{
					Raw: np.Value.Raw,
				},
			}

			switch np.Name {
			case root:
				endpoint.Spec.ClientTLS.RootCA.Value = dv
			case clientCert:
				endpoint.Spec.ClientTLS.Certificate.Value = dv
			case clientKey:
				endpoint.Spec.ClientTLS.PrivateKey.Value = dv
			}
		}
	}
}

func (p *Processor) preprocessOneComponent(ctx context.Context, comp *componentsapi.Component) componentPreprocessRes {
	_, unreadySecretsStore := p.secret.ProcessResource(ctx, comp)
	if unreadySecretsStore != "" {
		return componentPreprocessRes{
			unreadyDependency: componentDependency(components.CategorySecretStore, unreadySecretsStore),
		}
	}
	return componentPreprocessRes{}
}

func (p *Processor) category(comp componentsapi.Component) components.Category {
	for category := range p.managers {
		if strings.HasPrefix(comp.Spec.Type, string(category)+".") {
			return category
		}
	}
	return ""
}

func componentDependency(compCategory components.Category, name string) string {
	return string(compCategory) + ":" + name
}
