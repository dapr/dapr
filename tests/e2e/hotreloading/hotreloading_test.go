//go:build e2e
// +build e2e

/*
Copyright 2021 The Dapr Authors
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

package hotreloading_tests

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("hotreloading")
	utils.InitHTTPClient(false)

	testApps := []kube.AppDescription{
		{
			AppName:        "hotreloading-state",
			DaprEnabled:    true,
			ImageName:      "e2e-stateapp",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			Config:         "hotreloading",
		},
	}

	tr = runner.NewTestRunner("hotreloading", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestState(t *testing.T) {
	platform, ok := tr.Platform.(*runner.KubeTestPlatform)
	if !ok {
		t.Skip("skipping test; only supported on kubernetes")
	}

	scheme := runtime.NewScheme()
	assert.NoError(t, compapi.AddToScheme(scheme))
	assert.NoError(t, httpendapi.AddToScheme(scheme))

	cl, err := client.New(platform.KubeClient.GetClientConfig(), client.Options{Scheme: scheme})
	require.NoError(t, err)

	ctx := t.Context()

	externalURL := tr.Platform.AcquireAppExternalURL("hotreloading-state")
	// Wait for app to be available.
	_, err = utils.HTTPGetNTimes(externalURL, 60)
	require.NoError(t, err)

	const connectionString = `"host=dapr-postgres-postgresql.dapr-tests.svc.cluster.local user=postgres password=example port=5432 connect_timeout=10 database=dapr_test"`

	t.Run("Create state Component and save/get state", func(t *testing.T) {
		cl.Delete(ctx, &compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hotreloading-state",
				Namespace: kube.DaprTestNamespace,
			},
		}, &client.DeleteOptions{})

		require.NoError(t, cl.Create(ctx, &compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hotreloading-state",
				Namespace: kube.DaprTestNamespace,
			},
			Scoped: commonapi.Scoped{
				Scopes: []string{
					"hotreloading-state",
				},
			},
			Spec: compapi.ComponentSpec{
				Type:    "state.postgres",
				Version: "v1",
				Metadata: []commonapi.NameValuePair{
					{Name: "connectionString", Value: commonapi.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte(connectionString)}}},
					{Name: "table", Value: commonapi.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte(`"hotreloadtable"`)}}},
				},
			},
		}, &client.CreateOptions{}))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			url := fmt.Sprintf("%s/test/http/save/hotreloading-state", externalURL)
			_, status, err := utils.HTTPPostWithStatus(url, []byte(`{"states":[{"key":"foo","value":{"data":"LXcgYmFyCg=="}}]}`))
			assert.NoError(c, err)
			assert.Equal(c, http.StatusNoContent, status)
		}, 30*time.Second, 500*time.Millisecond)

		url := fmt.Sprintf("%s/test/http/get/hotreloading-state", externalURL)
		resp, code, err := utils.HTTPPostWithStatus(url, []byte(`{"states":[{"key":"foo"}]}`))
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, code)
		assert.Contains(t, string(resp), `{"states":[{"key":"foo","value":{"data":"LXcgYmFyCg=="},`)
	})

	t.Run("Update state component to another type and wait for it to become unavailable", func(t *testing.T) {
		var comp compapi.Component
		require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: kube.DaprTestNamespace, Name: "hotreloading-state"}, &comp))
		comp.Spec = compapi.ComponentSpec{
			Type:     "pubsub.in-memory",
			Version:  "v1",
			Metadata: []commonapi.NameValuePair{},
		}
		require.NoError(t, cl.Update(ctx, &comp))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			url := fmt.Sprintf("%s/test/http/save/hotreloading-state", externalURL)
			_, code, err := utils.HTTPPostWithStatus(url, []byte(`{"states":[{"key":"foo","value":{"data":"xyz"}}]}`))
			assert.NoError(c, err)
			assert.Equal(c, http.StatusBadRequest, code)
		}, 30*time.Second, 500*time.Millisecond)
	})

	t.Run("Update component to be state store again and be available", func(t *testing.T) {
		var comp compapi.Component
		require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: kube.DaprTestNamespace, Name: "hotreloading-state"}, &comp))
		comp.Spec = compapi.ComponentSpec{
			Type:    "state.postgres",
			Version: "v1",
			Metadata: []commonapi.NameValuePair{
				{Name: "connectionString", Value: commonapi.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte(connectionString)}}},
				{Name: "table", Value: commonapi.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte(`"hotreloadtable"`)}}},
			},
		}
		require.NoError(t, cl.Update(ctx, &comp))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			url := fmt.Sprintf("%s/test/http/save/hotreloading-state", externalURL)
			_, status, err := utils.HTTPPostWithStatus(url, []byte(`{"states":[{"key":"foo","value":{"data":"LXcgeHl6Cg=="}}]}`))
			assert.NoError(c, err)
			assert.Equal(c, http.StatusNoContent, status)
		}, 30*time.Second, 500*time.Millisecond)

		url := fmt.Sprintf("%s/test/http/get/hotreloading-state", externalURL)
		resp, code, err := utils.HTTPPostWithStatus(url, []byte(`{"states":[{"key":"foo"}]}`))
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, code)
		assert.Contains(t, string(resp), `{"states":[{"key":"foo","value":{"data":"LXcgeHl6Cg=="},`)
	})

	t.Run("Delete state Component and wait until no longer available", func(t *testing.T) {
		cl.Delete(ctx, &compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hotreloading-state",
				Namespace: kube.DaprTestNamespace,
			},
		}, &client.DeleteOptions{})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			url := fmt.Sprintf("%s/test/http/save/hotreloading-state", externalURL)
			_, code, err := utils.HTTPPostWithStatus(url, []byte(`{"states":[{"key":"foo","value":{"data":"LXcgYmFyCg=="}}]}`))
			assert.NoError(c, err)
			assert.Equal(c, http.StatusInternalServerError, code)
		}, 30*time.Second, 500*time.Millisecond)
	})
}
