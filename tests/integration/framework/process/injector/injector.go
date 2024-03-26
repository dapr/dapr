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

package injector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"

	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

type Injector struct {
	kubeapi  *kubernetes.Kubernetes
	exec     process.Interface
	freeport *ports.Ports

	port        int
	namespace   string
	metricsPort int
	healthzPort int
}

func New(t *testing.T, fopts ...Option) *Injector {
	t.Helper()

	fp := ports.Reserve(t, 3)
	opts := options{
		logLevel:      "info",
		enableMetrics: true,
		port:          fp.Port(t),
		metricsPort:   fp.Port(t),
		healthzPort:   fp.Port(t),
		sidecarImage:  "integration.dapr.io/dapr",
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	require.NotNil(t, opts.sentry, "sentry is required")
	require.NotNil(t, opts.namespace, "namespace is required")

	mobj, err := json.Marshal(new(admissionregistrationv1.MutatingWebhookConfiguration))
	require.NoError(t, err)
	kubeapi := kubernetes.New(t, kubernetes.WithBaseOperatorAPI(t,
		spiffeid.RequireTrustDomainFromString(opts.sentry.TrustDomain(t)),
		*opts.namespace,
		opts.sentry.Port(),
	),
		kubernetes.WithPath("/apis/admissionregistration.k8s.io/v1/mutatingwebhookconfigurations/dapr-sidecar-injector", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Content-Type", "application/json")
			w.Write(mobj)
		}),
	)

	args := []string{
		"-log-level=" + opts.logLevel,
		"-port=" + strconv.Itoa(opts.port),
		"-metrics-port=" + strconv.Itoa(opts.metricsPort),
		"-healthz-port=" + strconv.Itoa(opts.healthzPort),
		"-kubeconfig=" + kubeapi.KubeconfigPath(t),
	}

	return &Injector{
		kubeapi: kubeapi,
		exec: exec.New(t,
			binary.EnvValue("injector"), args,
			append(
				opts.execOpts,
				exec.WithEnvVars(t,
					"KUBERNETES_SERVICE_HOST", "anything",
					"NAMESPACE", *opts.namespace,
					"SIDECAR_IMAGE", opts.sidecarImage,
					"DAPR_TRUST_ANCHORS_FILE", opts.sentry.TrustAnchorsFile(t),
					"DAPR_CONTROL_PLANE_TRUST_DOMAIN", opts.sentry.TrustDomain(t),
					"DAPR_SENTRY_ADDRESS", opts.sentry.Address(),
				),
			)...,
		),
		freeport:    fp,
		port:        opts.port,
		metricsPort: opts.metricsPort,
		healthzPort: opts.healthzPort,
		namespace:   *opts.namespace,
	}
}

func (i *Injector) Run(t *testing.T, ctx context.Context) {
	i.kubeapi.Run(t, ctx)
	i.freeport.Free(t)
	i.exec.Run(t, ctx)
}

func (i *Injector) Cleanup(t *testing.T) {
	i.exec.Cleanup(t)
	i.freeport.Cleanup(t)
	i.kubeapi.Cleanup(t)
}

func (i *Injector) WaitUntilRunning(t *testing.T, ctx context.Context) {
	client := util.HTTPClient(t)
	//nolint:testifylint
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/healthz", i.healthzPort), nil)
		if !assert.NoError(t, err) {
			return
		}
		resp, err := client.Do(req)
		if !assert.NoError(t, err) {
			return
		}
		assert.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}, time.Second*5, 10*time.Millisecond)
}

func (i *Injector) Port() int {
	return i.port
}

func (i *Injector) Address() string {
	return "localhost:" + strconv.Itoa(i.port)
}

func (i *Injector) MetricsPort() int {
	return i.metricsPort
}

func (i *Injector) HealthzPort() int {
	return i.healthzPort
}
