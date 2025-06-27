/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package binding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(output))
}

type output struct {
	daprd    *daprd.Daprd
	operator *operator.Operator

	bindingDir1 string
	bindingDir2 string
	bindingDir3 string

	bindingDir1JSON common.DynamicValue
	bindingDir2JSON common.DynamicValue
	bindingDir3JSON common.DynamicValue
}

func (o *output) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	o.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)

	o.bindingDir1, o.bindingDir2, o.bindingDir3 = t.TempDir(), t.TempDir(), t.TempDir()

	dir1J, err := json.Marshal(o.bindingDir1)
	require.NoError(t, err)
	dir2J, err := json.Marshal(o.bindingDir2)
	require.NoError(t, err)
	dir3J, err := json.Marshal(o.bindingDir3)
	require.NoError(t, err)
	o.bindingDir1JSON = common.DynamicValue{JSON: apiextv1.JSON{Raw: dir1J}}
	o.bindingDir2JSON = common.DynamicValue{JSON: apiextv1.JSON{Raw: dir2J}}
	o.bindingDir3JSON = common.DynamicValue{JSON: apiextv1.JSON{Raw: dir3J}}

	o.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(o.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, o.operator, o.daprd),
	}
}

func (o *output) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)
	t.Run("expect no components to be loaded yet", func(t *testing.T) {
		assert.Empty(t, o.daprd.GetMetaRegisteredComponents(t, ctx))
	})

	t.Run("adding a component should become available", func(t *testing.T) {
		comp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "binding1",
				Namespace: "default",
			},
			Spec: compapi.ComponentSpec{
				Type:    "bindings.localstorage",
				Version: "v1",
				Metadata: []common.NameValuePair{{
					Name: "rootPath", Value: o.bindingDir1JSON,
				}},
			},
		}

		o.operator.SetComponents(comp)
		o.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, o.daprd.GetMetaRegisteredComponents(c, ctx), 1)
		}, time.Second*10, time.Millisecond*10)
		o.postBinding(t, ctx, client, "binding1", "file1", "data1")
		o.postBindingFail(t, ctx, client, "binding2")
		o.postBindingFail(t, ctx, client, "binding3")
		o.assertFile(t, o.bindingDir1, "file1", "data1")
	})

	t.Run("adding another component should become available", func(t *testing.T) {
		comp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "binding2",
				Namespace: "default",
			},
			Spec: compapi.ComponentSpec{
				Type:    "bindings.localstorage",
				Version: "v1",
				Metadata: []common.NameValuePair{{
					Name: "rootPath", Value: o.bindingDir2JSON,
				}},
			},
		}

		o.operator.AddComponents(comp)
		o.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, o.daprd.GetMetaRegisteredComponents(c, ctx), 2)
		}, time.Second*10, time.Millisecond*10)
		o.postBinding(t, ctx, client, "binding1", "file2", "data2")
		o.postBinding(t, ctx, client, "binding2", "file1", "data1")
		o.postBindingFail(t, ctx, client, "binding3")
		o.assertFile(t, o.bindingDir1, "file2", "data2")
		o.assertFile(t, o.bindingDir2, "file1", "data1")
	})

	t.Run("adding 3rd component should become available", func(t *testing.T) {
		comp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "binding3",
				Namespace: "default",
			},
			Spec: compapi.ComponentSpec{
				Type:    "bindings.localstorage",
				Version: "v1",
				Metadata: []common.NameValuePair{{
					Name: "rootPath", Value: o.bindingDir3JSON,
				}},
			},
		}
		o.operator.AddComponents(comp)
		o.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, o.daprd.GetMetaRegisteredComponents(c, ctx), 3)
		}, time.Second*10, time.Millisecond*10)
		o.postBinding(t, ctx, client, "binding1", "file3", "data3")
		o.postBinding(t, ctx, client, "binding2", "file2", "data2")
		o.postBinding(t, ctx, client, "binding3", "file1", "data1")
		o.assertFile(t, o.bindingDir1, "file3", "data3")
		o.assertFile(t, o.bindingDir2, "file2", "data2")
		o.assertFile(t, o.bindingDir3, "file1", "data1")
	})

	t.Run("deleting component makes it no longer available", func(t *testing.T) {
		comp := o.operator.Components()[0]
		o.operator.SetComponents(o.operator.Components()[1:]...)
		o.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_DELETED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, o.daprd.GetMetaRegisteredComponents(c, ctx), 2)
		}, time.Second*10, time.Millisecond*10)

		o.postBindingFail(t, ctx, client, "binding1")
		assert.NoFileExists(t, filepath.Join(o.bindingDir1, "file4"))
		o.postBinding(t, ctx, client, "binding2", "file3", "data3")
		o.postBinding(t, ctx, client, "binding3", "file2", "data2")
		o.assertFile(t, o.bindingDir2, "file3", "data3")
		o.assertFile(t, o.bindingDir3, "file2", "data2")
	})

	t.Run("deleting component files are no longer available", func(t *testing.T) {
		comp1 := o.operator.Components()[0]
		comp2 := o.operator.Components()[1]
		o.operator.SetComponents()
		o.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp1, EventType: operatorv1.ResourceEventType_DELETED})
		o.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp2, EventType: operatorv1.ResourceEventType_DELETED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, o.daprd.GetMetaRegisteredComponents(c, ctx))
		}, time.Second*10, time.Millisecond*10)
		o.postBindingFail(t, ctx, client, "binding1")
		o.postBindingFail(t, ctx, client, "binding2")
		o.postBindingFail(t, ctx, client, "binding3")
	})

	t.Run("recreating binding component should make it available again", func(t *testing.T) {
		comp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "binding2",
				Namespace: "default",
			},
			Spec: compapi.ComponentSpec{
				Type:    "bindings.localstorage",
				Version: "v1",
				Metadata: []common.NameValuePair{{
					Name: "rootPath", Value: o.bindingDir2JSON,
				}},
			},
		}
		o.operator.AddComponents(comp)
		o.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, o.daprd.GetMetaRegisteredComponents(c, ctx), 1)
		}, time.Second*10, time.Millisecond*10)
		o.postBinding(t, ctx, client, "binding2", "file5", "data5")
		o.postBindingFail(t, ctx, client, "binding1")
		o.postBindingFail(t, ctx, client, "binding3")
		o.assertFile(t, o.bindingDir2, "file5", "data5")
	})
}

func (o *output) postBinding(t *testing.T, ctx context.Context, client *http.Client, binding, file, data string) {
	t.Helper()

	url := fmt.Sprintf("http://localhost:%d/v1.0/bindings/%s", o.daprd.HTTPPort(), binding)
	body := fmt.Sprintf(`{"operation":"create","data":"%s","metadata":{"fileName":"%s"}}`, data, file)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respbody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(respbody))
}

func (o *output) postBindingFail(t *testing.T, ctx context.Context, client *http.Client, binding string) {
	t.Helper()

	url := fmt.Sprintf("http://localhost:%d/v1.0/bindings/%s", o.daprd.HTTPPort(), binding)
	body := `{"operation":"create","data":"foo","metadata":{"fileName":"foo"}}`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respbody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode, string(respbody))
}

func (o *output) assertFile(t *testing.T, dir, file, expData string) {
	t.Helper()
	fdata, err := os.ReadFile(filepath.Join(dir, file))
	require.NoError(t, err)
	assert.Equal(t, expData, string(fdata), fdata)
}
