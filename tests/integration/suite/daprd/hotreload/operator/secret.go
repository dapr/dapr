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

package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	rtpbv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(secret))
}

type secret struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
	client   *http.Client
	resDir1  string
	resDir2  string
	resDir3  string
}

func (s *secret) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	s.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)

	s.client = client.HTTP(t)

	s.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(s.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
			"FOO_SEC_1", "bar1",
			"FOO_SEC_2", "bar2",
			"FOO_SEC_3", "bar3",
			"BAR_SEC_1", "baz1",
			"BAR_SEC_2", "baz2",
			"BAR_SEC_3", "baz3",
			"BAZ_SEC_1", "foo1",
			"BAZ_SEC_2", "foo2",
			"BAZ_SEC_3", "foo3",
		)),
	)

	s.resDir1, s.resDir2, s.resDir3 = t.TempDir(), t.TempDir(), t.TempDir()

	return []framework.Option{
		framework.WithProcesses(sentry, s.operator, s.daprd),
	}
}

func (s *secret) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	t.Run("expect no components to be loaded yet", func(t *testing.T) {
		assert.Empty(t, s.daprd.GetMetaRegisteredComponents(t, ctx))
		s.readExpectError(t, ctx, "123", "SEC_1", http.StatusInternalServerError)
	})

	t.Run("adding a component should become available", func(t *testing.T) {
		newComp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "123"},
			Spec: compapi.ComponentSpec{
				Type:    "secretstores.local.env",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "PREFIX", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"FOO_"`)},
					}},
				},
			},
		}
		s.operator.SetComponents(newComp)
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, s.daprd.GetMetaRegisteredComponents(t, ctx), 1)
		}, time.Second*5, time.Millisecond*10)
		resp := s.daprd.GetMetaRegisteredComponents(t, ctx)
		require.Len(t, resp, 1)

		assert.ElementsMatch(t, resp, []*rtpbv1.RegisteredComponents{
			{Name: "123", Type: "secretstores.local.env", Version: "v1"},
		})

		s.read(t, ctx, "123", "SEC_1", "bar1")
		s.read(t, ctx, "123", "SEC_2", "bar2")
		s.read(t, ctx, "123", "SEC_3", "bar3")
	})

	t.Run("adding a second and third component should also become available", func(t *testing.T) {
		// After a single secret store exists, Dapr returns a Unauthorized response
		// rather than an Internal Server Error when writing to a non-existent
		// secret store.
		s.readExpectError(t, ctx, "abc", "2-sec-1", http.StatusUnauthorized)
		s.readExpectError(t, ctx, "xyz", "SEC_1", http.StatusUnauthorized)

		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2-sec.json"), []byte(`
 {
 "2-sec-1": "foo",
 "2-sec-2": "bar",
 "2-sec-3": "xxx"
 }
 `), 0o600))

		dir := filepath.Join(s.resDir2, "2-sec.json")
		dirJSON, err := json.Marshal(dir)
		require.NoError(t, err)

		newComp1 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "abc"},
			Spec: compapi.ComponentSpec{
				Type:    "secretstores.local.file",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "secretsFile", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: dirJSON},
					}},
				},
			},
		}

		newComp2 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "xyz"},
			Spec: compapi.ComponentSpec{
				Type:    "secretstores.local.env",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "PREFIX", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"BAR_"`)},
					}},
				},
			},
		}

		s.operator.AddComponents(newComp1, newComp2)
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp1, EventType: operatorv1.ResourceEventType_CREATED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp2, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, s.daprd.GetMetaRegisteredComponents(t, ctx), 3)
		}, time.Second*5, time.Millisecond*10)
		resp := s.daprd.GetMetaRegisteredComponents(t, ctx)
		require.Len(t, resp, 3)

		assert.ElementsMatch(t, []*rtpbv1.RegisteredComponents{
			{Name: "123", Type: "secretstores.local.env", Version: "v1"},
			{Name: "abc", Type: "secretstores.local.file", Version: "v1"},
			{Name: "xyz", Type: "secretstores.local.env", Version: "v1"},
		}, resp)

		s.read(t, ctx, "123", "SEC_1", "bar1")
		s.read(t, ctx, "123", "SEC_2", "bar2")
		s.read(t, ctx, "123", "SEC_3", "bar3")
		s.read(t, ctx, "abc", "2-sec-1", "foo")
		s.read(t, ctx, "abc", "2-sec-2", "bar")
		s.read(t, ctx, "abc", "2-sec-3", "xxx")
		s.read(t, ctx, "xyz", "SEC_1", "baz1")
		s.read(t, ctx, "xyz", "SEC_2", "baz2")
		s.read(t, ctx, "xyz", "SEC_3", "baz3")
	})

	t.Run("changing the type of a secret store should update the component and still be available", func(t *testing.T) {
		comp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "abc"},
			Spec: compapi.ComponentSpec{
				Type:    "secretstores.local.env",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "PREFIX", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"BAZ_"`)},
					}},
				},
			},
		}

		s.operator.SetComponents(s.operator.Components()[0], comp, s.operator.Components()[2])
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_UPDATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := s.daprd.GetMetaRegisteredComponents(t, ctx)
			assert.ElementsMatch(c, []*rtpbv1.RegisteredComponents{
				{Name: "123", Type: "secretstores.local.env", Version: "v1"},
				{Name: "abc", Type: "secretstores.local.env", Version: "v1"},
				{Name: "xyz", Type: "secretstores.local.env", Version: "v1"},
			}, resp)
		}, time.Second*5, time.Millisecond*10)

		s.read(t, ctx, "123", "SEC_1", "bar1")
		s.read(t, ctx, "123", "SEC_2", "bar2")
		s.read(t, ctx, "123", "SEC_3", "bar3")
		s.read(t, ctx, "abc", "SEC_1", "foo1")
		s.read(t, ctx, "abc", "SEC_2", "foo2")
		s.read(t, ctx, "abc", "SEC_3", "foo3")
		s.read(t, ctx, "xyz", "SEC_1", "baz1")
		s.read(t, ctx, "xyz", "SEC_2", "baz2")
		s.read(t, ctx, "xyz", "SEC_3", "baz3")
	})

	t.Run("updating multiple secret stores should be updated, and multiple components in a single file", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(s.resDir1, "1-sec.json"), []byte(`

 {
 "1-sec-1": "foo",
 "1-sec-2": "bar",
 "1-sec-3": "xxx"
 }
 `), 0o600))

		dir := filepath.Join(s.resDir1, "1-sec.json")
		dirJSON, err := json.Marshal(dir)
		require.NoError(t, err)

		comp1 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "123"},
			Spec: compapi.ComponentSpec{
				Type:    "secretstores.local.file",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "secretsFile", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: dirJSON},
					}},
				},
			},
		}
		comp2 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: compapi.ComponentSpec{
				Type:    "secretstores.local.env",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "PREFIX", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"BAZ_"`)},
					}},
				},
			},
		}

		dir = filepath.Join(s.resDir2, "2-sec.json")
		dirJSON, err = json.Marshal(dir)
		require.NoError(t, err)

		comp3 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "abc"},
			Spec: compapi.ComponentSpec{
				Type:    "secretstores.local.file",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "secretsFile", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: dirJSON},
					}},
				},
			},
		}

		s.operator.SetComponents(comp1, comp2, s.operator.Components()[2], comp3)
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp1, EventType: operatorv1.ResourceEventType_UPDATED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp2, EventType: operatorv1.ResourceEventType_UPDATED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp3, EventType: operatorv1.ResourceEventType_UPDATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := s.daprd.GetMetaRegisteredComponents(t, ctx)
			assert.ElementsMatch(c, []*rtpbv1.RegisteredComponents{
				{Name: "123", Type: "secretstores.local.file", Version: "v1"},
				{Name: "abc", Type: "secretstores.local.file", Version: "v1"},
				{Name: "xyz", Type: "secretstores.local.env", Version: "v1"},
				{Name: "foo", Type: "secretstores.local.env", Version: "v1"},
			}, resp)
		}, time.Second*5, time.Millisecond*10)

		s.read(t, ctx, "123", "1-sec-1", "foo")
		s.read(t, ctx, "123", "1-sec-2", "bar")
		s.read(t, ctx, "123", "1-sec-3", "xxx")
		s.read(t, ctx, "abc", "2-sec-1", "foo")
		s.read(t, ctx, "abc", "2-sec-2", "bar")
		s.read(t, ctx, "abc", "2-sec-3", "xxx")
		s.read(t, ctx, "xyz", "SEC_1", "baz1")
		s.read(t, ctx, "xyz", "SEC_2", "baz2")
		s.read(t, ctx, "xyz", "SEC_3", "baz3")
		s.read(t, ctx, "foo", "SEC_1", "foo1")
		s.read(t, ctx, "foo", "SEC_2", "foo2")
		s.read(t, ctx, "foo", "SEC_3", "foo3")
	})

	t.Run("renaming a component should close the old name, and open the new one", func(t *testing.T) {
		dir := filepath.Join(s.resDir2, "2-sec.json")
		dirJSON, err := json.Marshal(dir)
		require.NoError(t, err)

		comp1 := s.operator.Components()[3]
		comp2 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "bar"},
			Spec: compapi.ComponentSpec{
				Type:    "secretstores.local.file",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "secretsFile", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: dirJSON},
					}},
				},
			},
		}

		s.operator.SetComponents(s.operator.Components()[0], s.operator.Components()[1], s.operator.Components()[2], comp2)
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp1, EventType: operatorv1.ResourceEventType_DELETED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp2, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := s.daprd.GetMetaRegisteredComponents(t, ctx)
			assert.ElementsMatch(c, []*rtpbv1.RegisteredComponents{
				{Name: "123", Type: "secretstores.local.file", Version: "v1"},
				{Name: "bar", Type: "secretstores.local.file", Version: "v1"},
				{Name: "xyz", Type: "secretstores.local.env", Version: "v1"},
				{Name: "foo", Type: "secretstores.local.env", Version: "v1"},
			}, resp)
		}, time.Second*5, time.Millisecond*10)

		s.read(t, ctx, "123", "1-sec-1", "foo")
		s.read(t, ctx, "123", "1-sec-2", "bar")
		s.read(t, ctx, "123", "1-sec-3", "xxx")
		s.read(t, ctx, "bar", "2-sec-1", "foo")
		s.read(t, ctx, "bar", "2-sec-2", "bar")
		s.read(t, ctx, "bar", "2-sec-3", "xxx")
		s.read(t, ctx, "xyz", "SEC_1", "baz1")
		s.read(t, ctx, "xyz", "SEC_2", "baz2")
		s.read(t, ctx, "xyz", "SEC_3", "baz3")
		s.read(t, ctx, "foo", "SEC_1", "foo1")
		s.read(t, ctx, "foo", "SEC_2", "foo2")
		s.read(t, ctx, "foo", "SEC_3", "foo3")
	})

	t.Run("deleting a component (through type update) should delete the components", func(t *testing.T) {
		comp1 := s.operator.Components()[3]
		comp2 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "bar"},
			Spec: compapi.ComponentSpec{
				Type:    "state.in-memory",
				Version: "v1",
			},
		}

		s.operator.SetComponents(s.operator.Components()[0], s.operator.Components()[1], s.operator.Components()[2], comp2)
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp1, EventType: operatorv1.ResourceEventType_DELETED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp2, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := s.daprd.GetMetaRegisteredComponents(t, ctx)
			assert.ElementsMatch(c,
				[]*rtpbv1.RegisteredComponents{
					{Name: "123", Type: "secretstores.local.file", Version: "v1"},
					{Name: "xyz", Type: "secretstores.local.env", Version: "v1"},
					{Name: "foo", Type: "secretstores.local.env", Version: "v1"},
					{
						Name: "bar", Type: "state.in-memory", Version: "v1",
						Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
					},
				}, resp)
		}, time.Second*5, time.Millisecond*10)

		s.read(t, ctx, "123", "1-sec-1", "foo")
		s.read(t, ctx, "123", "1-sec-2", "bar")
		s.read(t, ctx, "123", "1-sec-3", "xxx")
		s.read(t, ctx, "xyz", "SEC_1", "baz1")
		s.read(t, ctx, "xyz", "SEC_2", "baz2")
		s.read(t, ctx, "xyz", "SEC_3", "baz3")
		s.read(t, ctx, "foo", "SEC_1", "foo1")
		s.read(t, ctx, "foo", "SEC_2", "foo2")
		s.read(t, ctx, "foo", "SEC_3", "foo3")
		s.readExpectError(t, ctx, "bar", "2-sec-1", http.StatusUnauthorized)
		s.readExpectError(t, ctx, "bar", "2-sec-2", http.StatusUnauthorized)
		s.readExpectError(t, ctx, "bar", "2-sec-3", http.StatusUnauthorized)
	})

	t.Run("deleting all components should result in no components remaining", func(t *testing.T) {
		comp1 := s.operator.Components()[0]
		comp2 := s.operator.Components()[1]
		comp3 := s.operator.Components()[2]
		comp4 := s.operator.Components()[3]
		s.operator.SetComponents(comp4)
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp1, EventType: operatorv1.ResourceEventType_DELETED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp2, EventType: operatorv1.ResourceEventType_DELETED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp3, EventType: operatorv1.ResourceEventType_DELETED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := s.daprd.GetMetaRegisteredComponents(t, ctx)
			assert.Len(c, resp, 1)
			assert.ElementsMatch(c, resp, []*rtpbv1.RegisteredComponents{
				{
					Name: "bar", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			})
		}, time.Second*5, time.Millisecond*10)

		s.readExpectError(t, ctx, "123", "1-sec-1", http.StatusInternalServerError)
		s.readExpectError(t, ctx, "xyz", "SEC_1", http.StatusInternalServerError)
		s.readExpectError(t, ctx, "bar", "2-sec-1", http.StatusInternalServerError)
		s.readExpectError(t, ctx, "foo", "SEC_1", http.StatusInternalServerError)
	})

	t.Run("recreating secret component should make it available again", func(t *testing.T) {
		comp1 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "123"},
			Spec: compapi.ComponentSpec{
				Type:    "secretstores.local.env",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "PREFIX", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"FOO_"`)},
					}},
				},
			},
		}
		s.operator.SetComponents(comp1, s.operator.Components()[0])
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp1, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, s.daprd.GetMetaRegisteredComponents(t, ctx), 2)
		}, time.Second*5, time.Millisecond*10)

		s.read(t, ctx, "123", "SEC_1", "bar1")
		s.read(t, ctx, "123", "SEC_2", "bar2")
		s.read(t, ctx, "123", "SEC_3", "bar3")
	})
}

func (s *secret) readExpectError(t *testing.T, ctx context.Context, compName, key string, expCode int) {
	t.Helper()

	getURL := fmt.Sprintf("http://localhost:%d/v1.0/secrets/%s/%s",
		s.daprd.HTTPPort(), url.QueryEscape(compName), url.QueryEscape(key))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	require.NoError(t, err)
	s.doReq(t, req, expCode, fmt.Sprintf(
		`\{"errorCode":"(ERR_SECRET_STORES_NOT_CONFIGURED|ERR_SECRET_STORE_NOT_FOUND)","message":"(secret store is not configured|failed finding secret store with key %s)"\}`,
		compName))
}

func (s *secret) read(t *testing.T, ctx context.Context, compName, key, expValue string) {
	t.Helper()

	getURL := fmt.Sprintf("http://localhost:%d/v1.0/secrets/%s/%s", s.daprd.HTTPPort(), url.QueryEscape(compName), url.QueryEscape(key))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	require.NoError(t, err)
	s.doReq(t, req, http.StatusOK, expValue)
}

func (s *secret) doReq(t *testing.T, req *http.Request, expCode int, expBody string) {
	t.Helper()

	resp, err := s.client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, expCode, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Regexp(t, expBody, string(body))
}
