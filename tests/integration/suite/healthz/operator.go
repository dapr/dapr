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

package healthz

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	procoperator "github.com/dapr/dapr/tests/integration/framework/process/operator"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(operator))
}

// operator tests Operator.
type operator struct {
	sentry *procsentry.Sentry
	proc   *procoperator.Operator
}

func (o *operator) Setup(t *testing.T) []framework.Option {
	o.sentry = procsentry.New(t, procsentry.WithTrustDomain("integration.test.dapr.io"))

	kubeAPI := kubernetes.New(t,
		kubernetes.WithDaprConfigurationGet(t, "dapr-system", "daprsystem", &configapi.Configuration{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
			ObjectMeta: metav1.ObjectMeta{Name: "daprsystem", Namespace: "default"},
			Spec: configapi.ConfigurationSpec{
				MTLSSpec: &configapi.MTLSSpec{
					ControlPlaneTrustDomain: "integration.test.dapr.io",
					SentryAddress:           "localhost:" + strconv.Itoa(o.sentry.Port()),
				},
			},
		}),
		kubernetes.WithClusterServiceList(t, new(corev1.ServiceList)),
		kubernetes.WithClusterStatefulSetList(t, new(appsv1.StatefulSetList)),
		kubernetes.WithClusterDeploymentList(t, new(appsv1.DeploymentList)),
		kubernetes.WithClusterDaprComponentList(t, new(compapi.ComponentList)),
		kubernetes.WithClusterDaprHTTPEndpointList(t, new(httpendapi.HTTPEndpointList)),
	)

	taf := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taf, o.sentry.CABundle().TrustAnchors, 0o600))
	o.proc = procoperator.New(t,
		procoperator.WithNamespace("dapr-system"),
		procoperator.WithKubeconfigPath(kubeAPI.KubeconfigPath(t)),
		procoperator.WithTrustAnchorsFile(taf),
	)

	return []framework.Option{
		framework.WithProcesses(kubeAPI, o.sentry, o.proc),
	}
}

func (o *operator) Run(t *testing.T, ctx context.Context) {
	o.sentry.WaitUntilRunning(t, ctx)
	o.proc.WaitUntilRunning(t, ctx)

	httpClient := util.HTTPClient(t)
	reqURL := fmt.Sprintf("http://localhost:%d/healthz", o.proc.HealthzPort())
	assert.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusOK
	}, time.Second*10, 100*time.Millisecond)
}
