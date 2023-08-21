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

package insecure

import (
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
)

func TestInsecure(t *testing.T) {
	var _ validator.Validator = New(spiffeid.TrustDomain{}, "")
}

func Test_maybeSetTrustDomain(t *testing.T) {
	tests := map[string]struct {
		req *sentryv1pb.SignCertificateRequest
		exp *sentryv1pb.SignCertificateRequest
	}{
		"if control plane service with same namespace, should set trust domain": {
			req: &sentryv1pb.SignCertificateRequest{
				Id:          "dapr-operator",
				TrustDomain: "",
				Namespace:   "dapr-system",
			},
			exp: &sentryv1pb.SignCertificateRequest{
				Id:          "dapr-operator",
				TrustDomain: "example.com",
				Namespace:   "dapr-system",
			},
		},
		"should not override trust domain if already set": {
			req: &sentryv1pb.SignCertificateRequest{
				Id:          "dapr-operator",
				TrustDomain: "foo",
				Namespace:   "dapr-system",
			},
			exp: &sentryv1pb.SignCertificateRequest{
				Id:          "dapr-operator",
				TrustDomain: "foo",
				Namespace:   "dapr-system",
			},
		},
		"should not set trust domain if not control plane service": {
			req: &sentryv1pb.SignCertificateRequest{
				Id:          "foo",
				TrustDomain: "",
				Namespace:   "dapr-system",
			},
			exp: &sentryv1pb.SignCertificateRequest{
				Id:          "foo",
				TrustDomain: "",
				Namespace:   "dapr-system",
			},
		},
		"should not set trust domain if not control plane namespace": {
			req: &sentryv1pb.SignCertificateRequest{
				Id:          "dapr-operator",
				TrustDomain: "",
				Namespace:   "foo",
			},
			exp: &sentryv1pb.SignCertificateRequest{
				Id:          "dapr-operator",
				TrustDomain: "",
				Namespace:   "foo",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			s := &insecure{
				cpNamespace:   "dapr-system",
				cpTrustDomain: spiffeid.RequireTrustDomainFromString("example.com"),
			}
			assert.Equal(t, test.exp, s.maybeSetTrustDomain(test.req))
		})
	}
}

func Test_isControlPlaneService(t *testing.T) {
	tests := map[string]struct {
		name string
		exp  bool
	}{
		"operator should be control plane service": {
			name: "dapr-operator",
			exp:  true,
		},
		"sentry should be control plane service": {
			name: "dapr-sentry",
			exp:  true,
		},
		"placement should be control plane service": {
			name: "dapr-placement",
			exp:  true,
		},
		"sidecar injector should be control plane service": {
			name: "dapr-injector",
			exp:  true,
		},
		"not a control plane service": {
			name: "my-app",
			exp:  false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, test.exp, isControlPlaneService(test.name))
		})
	}
}
