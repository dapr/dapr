/*
Copyright 2026 The Dapr Authors
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

package httpendpoint

import (
	"context"

	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"

	apiextapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/internal/apis"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
)

var log = logger.NewLogger("dapr.runtime.processor.loops.httpendpoint")

// SecretProcessor resolves secret references on a resource. Same shape the
// root and per-name MCPServer loops use; passed through so this package does
// not import the secret subsystem directly.
type SecretProcessor interface {
	ProcessResource(ctx context.Context, r meta.Resource) (bool, string)
}

// Options configures a per-name HTTPEndpoint loop.
type Options struct {
	Name      string
	CompStore *compstore.ComponentStore
	Secret    SecretProcessor
	// Root is the parent loop. The per-name loop posts HTTPEndpointAdded
	// back into it so root can update its in-flight counter.
	Root loop.Interface[loops.EventRoot]
}

// Handler is the loop.Handler[loops.EventHTTPEndpoint] for one HTTPEndpoint
// name. Secret resolution can hit a remote secret store, which is why each
// name runs on its own lane: one slow secret store cannot stall the root
// loop or starve other endpoints.
type Handler struct {
	name      string
	compStore *compstore.ComponentStore
	secret    SecretProcessor
	root      loop.Interface[loops.EventRoot]

	loop loop.Interface[loops.EventHTTPEndpoint]
}

// New constructs a per-name HTTPEndpoint loop handler. The caller must invoke
// Run on the returned Handler's Loop().
func New(opts Options) *Handler {
	h := &Handler{
		name:      opts.Name,
		compStore: opts.CompStore,
		secret:    opts.Secret,
		root:      opts.Root,
	}
	h.loop = loops.HTTPEndpointFactory.NewLoop(h)
	return h
}

// Loop returns the underlying per-name loop.
func (h *Handler) Loop() loop.Interface[loops.EventHTTPEndpoint] { return h.loop }

// Handle dispatches one event for this HTTPEndpoint name.
func (h *Handler) Handle(ctx context.Context, e loops.EventHTTPEndpoint) error {
	switch ev := e.(type) {
	case *loops.AddHTTPEndpoint:
		h.handleAdd(ctx, ev)
	case *loops.Shutdown:
		// HTTPEndpoints have no per-name state outside compstore, which
		// is torn down by daprd's main shutdown sequence. Nothing to do
		// here.
	}
	return nil
}

func (h *Handler) handleAdd(ctx context.Context, ev *loops.AddHTTPEndpoint) {
	defer h.root.Enqueue(&loops.HTTPEndpointAdded{})

	endpoint := ev.Endpoint
	if endpoint.Name == "" {
		sendResult(ev.Result, nil)
		return
	}
	h.processSecrets(ctx, &endpoint)
	h.compStore.AddHTTPEndpoint(endpoint)
	log.Debugf("HTTPEndpoint loaded: %s", endpoint.LogName())
	sendResult(ev.Result, nil)
}

func (h *Handler) processSecrets(ctx context.Context, endpoint *httpendpointsapi.HTTPEndpoint) {
	_, _ = h.secret.ProcessResource(ctx, endpoint)

	tlsResource := apis.GenericNameValueResource{
		Name:        endpoint.Name,
		Namespace:   endpoint.Namespace,
		SecretStore: endpoint.SecretStore,
		Pairs:       []commonapi.NameValuePair{},
	}

	root, clientCert, clientKey := "root", "clientCert", "clientKey"

	ca := commonapi.NameValuePair{Name: root}
	if endpoint.HasTLSRootCA() {
		ca.Value = *endpoint.Spec.ClientTLS.RootCA.Value
		tlsResource.Pairs = append(tlsResource.Pairs, ca)
	}
	if endpoint.HasTLSRootCASecret() {
		ca.SecretKeyRef = *endpoint.Spec.ClientTLS.RootCA.SecretKeyRef
		tlsResource.Pairs = append(tlsResource.Pairs, ca)
	}

	cCert := commonapi.NameValuePair{Name: clientCert}
	if endpoint.HasTLSClientCert() {
		cCert.Value = *endpoint.Spec.ClientTLS.Certificate.Value
		tlsResource.Pairs = append(tlsResource.Pairs, cCert)
	}
	if endpoint.HasTLSClientCertSecret() {
		cCert.SecretKeyRef = *endpoint.Spec.ClientTLS.Certificate.SecretKeyRef
		tlsResource.Pairs = append(tlsResource.Pairs, cCert)
	}

	cKey := commonapi.NameValuePair{Name: clientKey}
	if endpoint.HasTLSPrivateKey() {
		cKey.Value = *endpoint.Spec.ClientTLS.PrivateKey.Value
		tlsResource.Pairs = append(tlsResource.Pairs, cKey)
	}
	if endpoint.HasTLSPrivateKeySecret() {
		cKey.SecretKeyRef = *endpoint.Spec.ClientTLS.PrivateKey.SecretKeyRef
		tlsResource.Pairs = append(tlsResource.Pairs, cKey)
	}

	updated, _ := h.secret.ProcessResource(ctx, tlsResource)
	if !updated {
		return
	}
	for _, np := range tlsResource.Pairs {
		dv := &commonapi.DynamicValue{
			JSON: apiextapi.JSON{Raw: np.Value.Raw},
		}
		switch np.Name {
		case root:
			if endpoint.Spec.ClientTLS.RootCA == nil {
				continue
			}
			endpoint.Spec.ClientTLS.RootCA.Value = dv
		case clientCert:
			if endpoint.Spec.ClientTLS.Certificate == nil {
				endpoint.Spec.ClientTLS.Certificate = new(commonapi.TLSDocument)
			}
			endpoint.Spec.ClientTLS.Certificate.Value = dv
		case clientKey:
			if endpoint.Spec.ClientTLS.PrivateKey == nil {
				endpoint.Spec.ClientTLS.PrivateKey = new(commonapi.TLSDocument)
			}
			endpoint.Spec.ClientTLS.PrivateKey.Value = dv
		}
	}
}

func sendResult(ch chan<- error, err error) {
	if ch == nil {
		return
	}
	select {
	case ch <- err:
	default:
	}
}
