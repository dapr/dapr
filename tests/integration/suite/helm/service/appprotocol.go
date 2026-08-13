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

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(appprotocol))
}

// appprotocol asserts that every port of every rendered Service declares the
// expected appProtocol so service meshes can use explicit protocol selection.
type appprotocol struct {
	base          *helm.Helm
	withOIDC      *helm.Helm
	withOIDCNoTLS *helm.Helm
}

func (a *appprotocol) Setup(t *testing.T) []framework.Option {
	a.base = helm.New(t,
		helm.WithShowOnlyServices(t),
	)

	a.withOIDC = helm.New(t,
		helm.WithShowOnlyServices(t),
		helm.WithValues("dapr_sentry.oidc.enabled=true"),
	)

	a.withOIDCNoTLS = helm.New(t,
		helm.WithShowOnlyServices(t),
		helm.WithValues(
			"dapr_sentry.oidc.enabled=true",
			"dapr_sentry.oidc.tls.enabled=false",
		),
	)

	return []framework.Option{
		framework.WithProcesses(a.base, a.withOIDC, a.withOIDCNoTLS),
	}
}

func (a *appprotocol) Run(t *testing.T, ctx context.Context) {
	appProtocols := func(t *testing.T, svcs []corev1.Service) map[string]map[string]string {
		t.Helper()
		got := make(map[string]map[string]string, len(svcs))
		for _, svc := range svcs {
			ports := make(map[string]string, len(svc.Spec.Ports))
			for _, port := range svc.Spec.Ports {
				require.NotNilf(t, port.AppProtocol, "service %q port %q has no appProtocol", svc.Name, port.Name)
				ports[port.Name] = *port.AppProtocol
			}
			got[svc.Name] = ports
		}
		return got
	}

	t.Run("default", func(t *testing.T) {
		svcs := helm.UnmarshalStdout[corev1.Service](t, a.base)
		assert.Len(t, svcs, 7)
		assert.Equal(t, map[string]map[string]string{
			"dapr-api": {
				"grpc":    "tls",
				"legacy":  "tls",
				"metrics": "http",
			},
			"dapr-webhook": {
				"": "https",
			},
			"dapr-sentry": {
				"grpc":    "tls",
				"metrics": "http",
			},
			"dapr-sidecar-injector": {
				"https":   "https",
				"metrics": "http",
			},
			"dapr-placement-server": {
				"api":       "tls",
				"raft-node": "tls",
				"metrics":   "http",
			},
			"dapr-scheduler-server": {
				"api":         "tls",
				"etcd-client": "tcp",
				"etcd-peer":   "tls",
				"metrics":     "http",
			},
			"dapr-scheduler-server-a": {
				"api":         "tls",
				"etcd-client": "tcp",
				"etcd-peer":   "tls",
				"metrics":     "http",
			},
		}, appProtocols(t, svcs))
	})

	t.Run("sentry oidc with tls", func(t *testing.T) {
		svcs := helm.UnmarshalStdout[corev1.Service](t, a.withOIDC)
		got := appProtocols(t, svcs)
		assert.Equal(t, map[string]string{
			"grpc":    "tls",
			"metrics": "http",
			"oidc":    "https",
		}, got["dapr-sentry"])
	})

	t.Run("sentry oidc without tls", func(t *testing.T) {
		svcs := helm.UnmarshalStdout[corev1.Service](t, a.withOIDCNoTLS)
		got := appProtocols(t, svcs)
		assert.Equal(t, map[string]string{
			"grpc":    "tls",
			"metrics": "http",
			"oidc":    "http",
		}, got["dapr-sentry"])
	})
}
