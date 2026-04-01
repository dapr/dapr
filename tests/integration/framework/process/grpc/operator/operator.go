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

package operator

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/security"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
)

// Option is a function that configures the process.
type Option func(*options)

// Operator is a wrapper around a grpc.Server that implements the Operator API.
type Operator struct {
	*procgrpc.GRPC

	closech              chan struct{}
	lock                 sync.RWMutex
	updateCompCh         chan *api.ComponentUpdateEvent
	updateSubCh          chan *SubscriptionUpdateEvent
	updateMCPCh          chan *MCPServerUpdateEvent
	srvCompUpdateCh      []chan *api.ComponentUpdateEvent
	srvSubUpdateCh       []chan *SubscriptionUpdateEvent
	srvMCPUpdateCh       []chan *MCPServerUpdateEvent
	currentComponents    []compapi.Component
	currentSubscriptions []subapi.Subscription
	currentMCPServers    []mcpserverapi.MCPServer
}

type SubscriptionUpdateEvent struct {
	Subscription *subapi.Subscription
	EventType    operatorv1.ResourceEventType
}

type MCPServerUpdateEvent struct {
	MCPServer *mcpserverapi.MCPServer
	EventType operatorv1.ResourceEventType
}

func New(t *testing.T, fopts ...Option) *Operator {
	t.Helper()

	o := &Operator{
		closech:      make(chan struct{}),
		updateCompCh: make(chan *api.ComponentUpdateEvent),
		updateSubCh:  make(chan *SubscriptionUpdateEvent),
		updateMCPCh:  make(chan *MCPServerUpdateEvent),
	}

	opts := options{
		listComponentsFn: func(ctx context.Context, req *operatorv1.ListComponentsRequest) (*operatorv1.ListComponentResponse, error) {
			o.lock.Lock()
			defer o.lock.Unlock()
			var comps [][]byte
			for _, comp := range o.currentComponents {
				if comp.Namespace != req.GetNamespace() {
					continue
				}
				compB, err := json.Marshal(comp)
				if err != nil {
					return nil, err
				}
				comps = append(comps, compB)
			}
			return &operatorv1.ListComponentResponse{Components: comps}, nil
		},
		componentUpdateFn: func(req *operatorv1.ComponentUpdateRequest, srv operatorv1.Operator_ComponentUpdateServer) error {
			o.lock.Lock()
			updateCh := make(chan *api.ComponentUpdateEvent)
			o.srvCompUpdateCh = append(o.srvCompUpdateCh, updateCh)
			o.lock.Unlock()

			for {
				select {
				case <-srv.Context().Done():
					return nil
				case <-o.closech:
					return errors.New("operator closed")
				case comp := <-updateCh:
					if len(comp.Component.Namespace) == 0 {
						comp.Component.Namespace = "default"
					}
					if comp.Component.Namespace != req.GetNamespace() {
						continue
					}

					compB, err := json.Marshal(comp.Component)
					if err != nil {
						return err
					}

					if err := srv.Send(&operatorv1.ComponentUpdateEvent{
						Component: compB,
						Type:      comp.EventType,
					}); err != nil {
						return err
					}
				}
			}
		},
		subscriptionUpdateFn: func(req *operatorv1.SubscriptionUpdateRequest, srv operatorv1.Operator_SubscriptionUpdateServer) error {
			o.lock.Lock()
			updateCh := make(chan *SubscriptionUpdateEvent)
			o.srvSubUpdateCh = append(o.srvSubUpdateCh, updateCh)
			o.lock.Unlock()

			for {
				select {
				case <-srv.Context().Done():
					return nil
				case <-o.closech:
					return errors.New("operator closed")
				case sub := <-updateCh:
					if len(sub.Subscription.Namespace) == 0 {
						sub.Subscription.Namespace = "default"
					}
					if sub.Subscription.Namespace != req.GetNamespace() {
						continue
					}

					subB, err := json.Marshal(sub.Subscription)
					if err != nil {
						return err
					}

					if err := srv.Send(&operatorv1.SubscriptionUpdateEvent{
						Subscription: subB,
						Type:         sub.EventType,
					}); err != nil {
						return err
					}
				}
			}
		},
		listMCPServersFn: func(ctx context.Context, req *operatorv1.ListMCPServersRequest) (*operatorv1.ListMCPServersResponse, error) {
			o.lock.Lock()
			defer o.lock.Unlock()
			var servers [][]byte
			for _, s := range o.currentMCPServers {
				if s.Namespace != req.GetNamespace() {
					continue
				}
				b, err := json.Marshal(s)
				if err != nil {
					return nil, err
				}
				servers = append(servers, b)
			}
			return &operatorv1.ListMCPServersResponse{McpServers: servers}, nil
		},
		mcpServerUpdateFn: func(req *operatorv1.MCPServerUpdateRequest, srv operatorv1.Operator_MCPServerUpdateServer) error {
			o.lock.Lock()
			updateCh := make(chan *MCPServerUpdateEvent)
			o.srvMCPUpdateCh = append(o.srvMCPUpdateCh, updateCh)
			o.lock.Unlock()

			for {
				select {
				case <-srv.Context().Done():
					return nil
				case <-o.closech:
					return errors.New("operator closed")
				case mcp := <-updateCh:
					if len(mcp.MCPServer.Namespace) == 0 {
						mcp.MCPServer.Namespace = "default"
					}
					if mcp.MCPServer.Namespace != req.GetNamespace() {
						continue
					}

					b, err := json.Marshal(mcp.MCPServer)
					if err != nil {
						return err
					}

					if err := srv.Send(&operatorv1.MCPServerUpdateEvent{
						McpServer: b,
						Type:      mcp.EventType,
					}); err != nil {
						return err
					}
				}
			}
		},
		configurationUpdateFn: func(_ *operatorv1.ConfigurationUpdateRequest, srv operatorv1.Operator_ConfigurationUpdateServer) error {
			select {
			case <-srv.Context().Done():
				return nil
			case <-o.closech:
				return errors.New("operator closed")
			}
		},
		httpEndpointUpdateFn: func(_ *operatorv1.HTTPEndpointUpdateRequest, srv operatorv1.Operator_HTTPEndpointUpdateServer) error {
			select {
			case <-srv.Context().Done():
				return nil
			case <-o.closech:
				return errors.New("operator closed")
			}
		},
		resiliencyUpdateFn: func(_ *operatorv1.ResiliencyUpdateRequest, srv operatorv1.Operator_ResiliencyUpdateServer) error {
			select {
			case <-srv.Context().Done():
				return nil
			case <-o.closech:
				return errors.New("operator closed")
			}
		},
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	require.NotNil(t, opts.sentry, "must provide sentry")

	o.GRPC = procgrpc.New(t, append(opts.grpcopts,
		procgrpc.WithServerOption(func(t *testing.T, ctx context.Context) grpc.ServerOption {
			secProv, err := security.New(ctx, security.Options{
				SentryAddress:           "localhost:" + strconv.Itoa(opts.sentry.Port()),
				ControlPlaneTrustDomain: "localhost",
				ControlPlaneNamespace:   "default",
				TrustAnchors:            opts.sentry.CABundle().X509.TrustAnchors,
				AppID:                   "dapr-operator",
				MTLSEnabled:             true,
				Healthz:                 healthz.New(),
			})
			require.NoError(t, err)

			secProvErr := make(chan error)
			t.Cleanup(func() {
				select {
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for security provider to stop")
				case err = <-secProvErr:
					require.NoError(t, err)
				}
			})
			go func() {
				secProvErr <- secProv.Run(ctx)
			}()

			sec, err := secProv.Handler(ctx)
			require.NoError(t, err)

			return sec.GRPCServerOptionMTLS()
		}),
		procgrpc.WithRegister(func(s *grpc.Server) {
			srv := &server{
				componentUpdateFn:            opts.componentUpdateFn,
				configurationUpdateFn:        opts.configurationUpdateFn,
				getConfigurationFn:           opts.getConfigurationFn,
				getResiliencyFn:              opts.getResiliencyFn,
				httpEndpointUpdateFn:         opts.httpEndpointUpdateFn,
				listComponentsFn:             opts.listComponentsFn,
				listHTTPEndpointsFn:          opts.listHTTPEndpointsFn,
				listMCPServersFn:             opts.listMCPServersFn,
				listResiliencyFn:             opts.listResiliencyFn,
				listSubscriptionsFn:          opts.listSubscriptionsFn,
				listSubscriptionsV2Fn:        opts.listSubscriptionsV2Fn,
				mcpServerUpdateFn:            opts.mcpServerUpdateFn,
				resiliencyUpdateFn:           opts.resiliencyUpdateFn,
				subscriptionUpdateFn:         opts.subscriptionUpdateFn,
				workflowAccessPolicyUpdateFn: opts.workflowAccessPolicyUpdateFn,
			}

			operatorv1.RegisterOperatorServer(s, srv)
			if opts.withRegister != nil {
				opts.withRegister(s)
			}
		}))...)

	return o
}

func (o *Operator) Cleanup(t *testing.T) {
	close(o.closech)
	o.GRPC.Cleanup(t)
}

// Add Component adds a component to the publish list of installed components.
func (o *Operator) AddComponents(cs ...compapi.Component) {
	o.lock.Lock()
	defer o.lock.Unlock()
	o.currentComponents = append(o.currentComponents, cs...)
}

// SetComponents sets the list of installed components.
func (o *Operator) SetComponents(cs ...compapi.Component) {
	o.lock.Lock()
	defer o.lock.Unlock()
	o.currentComponents = cs
}

// Components returns the list of installed components.
func (o *Operator) Components() []compapi.Component {
	o.lock.RLock()
	defer o.lock.RUnlock()
	return o.currentComponents
}

// ComponentUpdateEvent sends a component update event to the operator which
// will be piped to clients listening on ComponentUpdate.
func (o *Operator) ComponentUpdateEvent(t *testing.T, ctx context.Context, event *api.ComponentUpdateEvent) {
	t.Helper()
	o.lock.Lock()
	defer o.lock.Unlock()

	for _, ch := range o.srvCompUpdateCh {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for component update event")
		case <-o.closech:
			t.Fatal("operator closed")
		case ch <- event:
		}
	}
}

func (o *Operator) AddSubscriptions(subs ...subapi.Subscription) {
	o.lock.Lock()
	defer o.lock.Unlock()
	o.currentSubscriptions = append(o.currentSubscriptions, subs...)
}

func (o *Operator) SubscriptionUpdateEvent(t *testing.T, ctx context.Context, event *SubscriptionUpdateEvent) {
	t.Helper()
	o.lock.Lock()
	defer o.lock.Unlock()

	for _, ch := range o.srvSubUpdateCh {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for subscption update event")
		case <-o.closech:
			t.Fatal("operator closed")
		case ch <- event:
		}
	}
}

func (o *Operator) Subscriptions() []subapi.Subscription {
	o.lock.RLock()
	defer o.lock.RUnlock()
	return o.currentSubscriptions
}

func (o *Operator) SetSubscriptions(subs ...subapi.Subscription) {
	o.lock.Lock()
	defer o.lock.Unlock()
	o.currentSubscriptions = subs
}

func (o *Operator) AddMCPServers(servers ...mcpserverapi.MCPServer) {
	o.lock.Lock()
	defer o.lock.Unlock()
	o.currentMCPServers = append(o.currentMCPServers, servers...)
}

func (o *Operator) MCPServerUpdateEvent(t *testing.T, ctx context.Context, event *MCPServerUpdateEvent) {
	t.Helper()
	o.lock.Lock()
	defer o.lock.Unlock()

	for _, ch := range o.srvMCPUpdateCh {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for MCPServer update event")
		case <-o.closech:
			t.Fatal("operator closed")
		case ch <- event:
		}
	}
}
