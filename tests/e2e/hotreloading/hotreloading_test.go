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
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

var (
	tr *runner.TestRunner
)

func TestMain(m *testing.M) {
	utils.SetupLogs("service_invocation")
	utils.InitHTTPClient(false)

	testApps := []kube.AppDescription{
		{
			AppName:        "hotreloading-caller",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			Config:         "hotreloading",
		},
		{
			AppName:        "hotreloading-callee",
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			MetricsEnabled: true,
			Config:         "hotreloading",
		},
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

func TestHTTPEndpoints(t *testing.T) {
	platform, ok := tr.Platform.(*runner.KubeTestPlatform)
	if !ok {
		t.Skip("skipping test; only supported on kubernetes")
	}

	cl, err := client.New(platform.KubeClient.GetClientConfig(), client.Options{})
	require.NoError(t, err)

	ctx := context.Background()

	externalURL := tr.Platform.AcquireAppExternalURL("hotreloading-caller")

	t.Run("Create HTTPEndpoint for callee and wait until reachable", func(t *testing.T) {
		cl.Delete(ctx, &httpendapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hotreloading",
				Namespace: kube.DaprTestNamespace,
			},
		}, &client.DeleteOptions{})

		require.NoError(t, cl.Create(ctx, &httpendapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hotreloading",
				Namespace: kube.DaprTestNamespace,
			},
			Spec: httpendapi.HTTPEndpointSpec{
				BaseURL: "http://hotreloading-callee:3000",
				Headers: []commonapi.NameValuePair{
					{Name: "foo", Value: commonapi.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte("bar")}}},
					{Name: "bar", Value: commonapi.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte("baz")}}},
				},
			},
		}, &client.CreateOptions{}))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			url := fmt.Sprintf("http://%s/simple-endpoint-call", externalURL)
			resp, err := utils.HTTPGetRaw(url)
			assert.NoError(c, err)
			if resp != nil {
				assert.Equal(c, http.StatusOK, resp.StatusCode)
				assert.Equal(c, "bar", resp.Header.Get("foo"))
				assert.Equal(c, "baz", resp.Header.Get("bar"))
			}
		}, 15*time.Second, 500*time.Millisecond)
	})

	t.Run("Update HTTPEndpoint and expect new headers to be returned", func(t *testing.T) {
		require.NoError(t, cl.Update(ctx, &httpendapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hotreloading",
				Namespace: kube.DaprTestNamespace,
			},
			Spec: httpendapi.HTTPEndpointSpec{
				BaseURL: "http://hotreloading-callee:3000",
				Headers: []commonapi.NameValuePair{
					{Name: "123", Value: commonapi.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte("abc")}}},
					{Name: "456", Value: commonapi.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte("def")}}},
				},
			},
		}, &client.UpdateOptions{}))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			url := fmt.Sprintf("http://%s/simple-endpoint-call", externalURL)
			resp, err := utils.HTTPGetRaw(url)
			assert.NoError(c, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(c, "123", resp.Header.Get("abc"))
			assert.Equal(c, "456", resp.Header.Get("def"))
		}, 15*time.Second, 500*time.Millisecond)
	})

	t.Run("Delete HTTPEndpoint and expect endpoint to no longer be reachable", func(t *testing.T) {
		require.NoError(t, cl.Delete(ctx, &httpendapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hotreloading",
				Namespace: kube.DaprTestNamespace,
			},
		}, &client.DeleteOptions{}))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			url := fmt.Sprintf("http://%s/simple-endpoint-call", externalURL)
			resp, err := utils.HTTPGetRaw(url)
			assert.NoError(c, err)
			assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		}, 15*time.Second, 500*time.Millisecond)
	})
}

func TestState(t *testing.T) {
	platform, ok := tr.Platform.(*runner.KubeTestPlatform)
	if !ok {
		t.Skip("skipping test; only supported on kubernetes")
	}

	cl, err := client.New(platform.KubeClient.GetClientConfig(), client.Options{})
	require.NoError(t, err)

	ctx := context.Background()

	externalURL := tr.Platform.AcquireAppExternalURL("hotreloading-caller")

	const connectionString = "host=dapr-postgres-postgresql.dapr-tests.svc.cluster.local user=postgres password=example port=5432 connect_timeout=10 database=dapr_test"

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
			url := fmt.Sprintf("http://%s/http/save/hotreloading-state", externalURL)
			_, status, err := utils.HTTPPostWithStatus(url, []byte(`{"key": "foo", "value": "bar"}`))
			assert.NoError(c, err)
			assert.Equal(c, http.StatusNoContent, status)
		}, 15*time.Second, 500*time.Millisecond)

		url := fmt.Sprintf("http://%s/http/get/hotreloading-state", externalURL)
		resp, code, err := utils.HTTPGetWithStatusWithData(url, []byte(`{"key": "foo"}`))
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, code)
		assert.Equal(t, "bar", string(resp))
	})

	t.Run("Delete state Component and wait until no longer available", func(t *testing.T) {
		cl.Delete(ctx, &compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hotreloading-state",
				Namespace: kube.DaprTestNamespace,
			},
		}, &client.DeleteOptions{})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			url := fmt.Sprintf("http://%s/http/save/hotreloading-state", externalURL)
			_, code, err := utils.HTTPPostWithStatus(url, []byte(`{"key": "foo", "value": "bar"}`))
			assert.NoError(c, err)
			assert.Equal(c, http.StatusInternalServerError, code)
		}, 15*time.Second, 500*time.Millisecond)
	})
}
