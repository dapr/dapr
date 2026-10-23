//go:build e2e

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

package operator_scopes_e2e

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

// The test apps call the operator API directly with the workload identity
// Sentry issues to their own app ID, as any Dapr-enabled pod can. Resources in
// tests/config/operator_scopes.yaml are scoped to one app each, and the
// opscopes-otherapp ones reference opscopes-secret.
const (
	myApp    = "opscopes-myapp"
	otherApp = "opscopes-otherapp"
)

var (
	tr          *runner.TestRunner
	otherSecret = base64.StdEncoding.EncodeToString([]byte("opscopes-otherapp-only"))
)

func TestMain(m *testing.M) {
	utils.SetupLogs("operator_scopes")
	utils.InitHTTPClient(true)

	// The apps mount the dapr-trust-bundle ConfigMap and address the control
	// plane in the test namespace, which tests/dapr_tests.mk defaults to
	// DAPR_NAMESPACE.
	ns := kube.DaprTestNamespace
	tokenExpiration := int64(3600)
	app := func(appID string) kube.AppDescription {
		return kube.AppDescription{
			AppName:           appID,
			DaprEnabled:       true,
			ImageName:         "e2e-operatorscopes",
			Replicas:          1,
			IngressEnabled:    true,
			Config:            appID,
			DaprMemoryLimit:   "200Mi",
			DaprMemoryRequest: "100Mi",
			AppMemoryLimit:    "200Mi",
			AppMemoryRequest:  "100Mi",
			AppEnv: map[string]string{ //nolint:gosec
				"APP_ID":                     appID,
				"NAMESPACE":                  ns,
				"SENTRY_ADDRESS":             "dapr-sentry." + ns + ".svc.cluster.local:443",
				"OPERATOR_ADDRESS":           "dapr-api." + ns + ".svc.cluster.local:443",
				"CONTROL_PLANE_TRUST_DOMAIN": "cluster.local",
				"TRUST_ANCHORS_FILE":         "/var/run/secrets/opscopes/trust/ca.crt",
				"DAPR_SENTRY_TOKEN_FILE":     "/var/run/secrets/opscopes/token/token",
			},
			Volumes: []apiv1.Volume{
				{
					Name: "opscopes-token",
					VolumeSource: apiv1.VolumeSource{Projected: &apiv1.ProjectedVolumeSource{
						Sources: []apiv1.VolumeProjection{{ServiceAccountToken: &apiv1.ServiceAccountTokenProjection{
							Audience:          "spiffe://cluster.local/ns/" + ns + "/dapr-sentry",
							ExpirationSeconds: &tokenExpiration,
							Path:              "token",
						}}},
					}},
				},
				{
					Name: "opscopes-trust",
					VolumeSource: apiv1.VolumeSource{ConfigMap: &apiv1.ConfigMapVolumeSource{
						LocalObjectReference: apiv1.LocalObjectReference{Name: "dapr-trust-bundle"},
					}},
				},
			},
			AppVolumeMounts: []apiv1.VolumeMount{
				{Name: "opscopes-token", MountPath: "/var/run/secrets/opscopes/token", ReadOnly: true},
				{Name: "opscopes-trust", MountPath: "/var/run/secrets/opscopes/trust", ReadOnly: true},
			},
		}
	}

	tr = runner.NewTestRunner("operator_scopes", []kube.AppDescription{app(myApp), app(otherApp)}, nil, nil)
	os.Exit(tr.Start(m))
}

func TestOperatorScopes(t *testing.T) {
	myURL := tr.Platform.AcquireAppExternalURL(myApp)
	otherURL := tr.Platform.AcquireAppExternalURL(otherApp)
	require.NoError(t, utils.HealthCheckApps(myURL, otherURL))

	for _, kind := range []string{"httpendpoints", "mcpservers"} {
		t.Run(kind+" scoped to another app are not disclosed", func(t *testing.T) {
			names, body := list(t, myURL+"/"+kind)
			assert.Contains(t, names, myApp)
			assert.NotContains(t, names, otherApp)
			assert.NotContains(t, body, otherSecret)
		})

		t.Run(kind+" scoped to the app are returned with secrets", func(t *testing.T) {
			names, body := list(t, otherURL+"/"+kind)
			assert.Contains(t, names, otherApp)
			assert.NotContains(t, names, myApp)
			assert.Contains(t, body, otherSecret)
		})
	}

	t.Run("configuration assigned to the app is returned", func(t *testing.T) {
		_, status, err := utils.HTTPGetWithStatus(myURL + "/configurations/" + myApp)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, status)
	})

	t.Run("configuration assigned to another app is denied", func(t *testing.T) {
		_, status, err := utils.HTTPGetWithStatus(myURL + "/configurations/" + otherApp)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, status)
	})
}

// list returns the names of the resources the app received from the operator
// list RPC behind url, and the raw response body.
func list(t *testing.T, url string) ([]string, string) {
	t.Helper()

	var body []byte
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var status int
		var err error
		body, status, err = utils.HTTPGetWithStatus(url)
		assert.NoError(c, err)
		assert.Equal(c, http.StatusOK, status, string(body))
	}, time.Second*30, time.Second)

	var items []metav1.PartialObjectMetadata
	require.NoError(t, json.Unmarshal(body, &items))
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names, string(body)
}
