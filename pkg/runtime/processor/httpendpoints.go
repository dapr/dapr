/*
Copyright 2024 The Dapr Authors
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

	apiextapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/internal/apis"
)

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
