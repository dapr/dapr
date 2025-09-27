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
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(state))
}

type state struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
}

func (s *state) Setup(t *testing.T) []framework.Option {
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

	s.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(s.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, s.operator, s.daprd),
	}
}

func (s *state) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	t.Run("expect no components to be loaded yet", func(t *testing.T) {
		assert.Empty(t, s.daprd.GetMetaRegisteredComponents(t, ctx))
		s.writeExpectError(t, ctx, client, "123", http.StatusInternalServerError)
	})

	t.Run("adding a component should become available", func(t *testing.T) {
		newComp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "123"},
			Spec: compapi.ComponentSpec{
				Type:    "state.in-memory",
				Version: "v1",
			},
		}
		s.operator.SetComponents(newComp)
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, s.daprd.GetMetaRegisteredComponents(t, ctx), 1)
		}, time.Second*5, time.Millisecond*10)
		resp := s.daprd.GetMetaRegisteredComponents(t, ctx)
		require.Len(t, resp, 1)

		assert.ElementsMatch(t, resp, []*rtv1.RegisteredComponents{
			{
				Name:    "123",
				Type:    "state.in-memory",
				Version: "v1",
				Capabilities: []string{
					"ETAG",
					"TRANSACTIONAL",
					"TTL",
					"DELETE_WITH_PREFIX",
					"ACTOR",
				},
			},
		})

		s.writeRead(t, ctx, client, "123")
	})

	t.Run("adding a second and third component should also become available", func(t *testing.T) {
		// After a single state store exists, Dapr returns a Bad Request response
		// rather than an Internal Server Error when writing to a non-existent
		// state store.
		s.writeExpectError(t, ctx, client, "abc", http.StatusBadRequest)
		s.writeExpectError(t, ctx, client, "xyz", http.StatusBadRequest)

		newComp1 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "abc"},
			Spec: compapi.ComponentSpec{
				Type:    "state.in-memory",
				Version: "v1",
			},
		}
		newComp2 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "xyz"},
			Spec: compapi.ComponentSpec{
				Type:    "state.in-memory",
				Version: "v1",
			},
		}
		s.operator.AddComponents(newComp1, newComp2)

		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp1, EventType: operatorv1.ResourceEventType_CREATED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp2, EventType: operatorv1.ResourceEventType_CREATED})

		var resp []*rtv1.RegisteredComponents
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp = s.daprd.GetMetaRegisteredComponents(t, ctx)
			assert.Len(c, resp, 3)
		}, time.Second*5, time.Millisecond*10)

		assert.ElementsMatch(t, []*rtv1.RegisteredComponents{
			{
				Name: "123", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
			},
			{
				Name: "abc", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
			},
			{
				Name: "xyz", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
			},
		}, resp)

		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "abc")
		s.writeRead(t, ctx, client, "xyz")
	})

	tmpDir := t.TempDir()
	t.Run("changing the type of a state store should update the component and still be available", func(t *testing.T) {
		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "abc")
		s.writeRead(t, ctx, client, "xyz")

		dbPath := filepath.Join(tmpDir, "db.sqlite")
		dbPathJSON, err := json.Marshal(dbPath)
		require.NoError(t, err)

		newComp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "abc"},
			Spec: compapi.ComponentSpec{
				Type:    "state.sqlite",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "connectionString", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: dbPathJSON},
					}},
				},
			},
		}
		s.operator.SetComponents(s.operator.Components()[0], newComp, s.operator.Components()[2])
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_UPDATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := s.daprd.GetMetaRegisteredComponents(c, ctx)
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{
					Name: "123", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
				{
					Name: "abc", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "xyz", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			}, resp)
		}, time.Second*5, time.Millisecond*10)

		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "abc")
		s.writeRead(t, ctx, client, "xyz")
	})

	t.Run("updating multiple state stores should be updated", func(t *testing.T) {
		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "abc")
		s.writeRead(t, ctx, client, "xyz")
		s.writeExpectError(t, ctx, client, "foo", http.StatusBadRequest)

		dbPath := filepath.Join(tmpDir, "db.sqlite")
		dbPathJSON, err := json.Marshal(dbPath)
		require.NoError(t, err)

		comp1 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "123"},
			Spec: compapi.ComponentSpec{
				Type:    "state.sqlite",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "connectionString", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: dbPathJSON},
					}},
				},
			},
		}
		comp2 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "abc"},
			Spec: compapi.ComponentSpec{
				Type:    "state.in-memory",
				Version: "v1",
			},
		}
		comp3 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "xyz"},
			Spec: compapi.ComponentSpec{
				Type:    "state.in-memory",
				Version: "v1",
			},
		}
		comp4 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: compapi.ComponentSpec{
				Type:    "state.in-memory",
				Version: "v1",
			},
		}

		s.operator.SetComponents(comp1, comp2, comp3, comp4)
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp1, EventType: operatorv1.ResourceEventType_UPDATED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp2, EventType: operatorv1.ResourceEventType_UPDATED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp3, EventType: operatorv1.ResourceEventType_UPDATED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp4, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := s.daprd.GetMetaRegisteredComponents(c, ctx)
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{
					Name: "123", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "abc", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
				{
					Name: "xyz", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
				{
					Name: "foo", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			}, resp)
		}, time.Second*5, time.Millisecond*10)

		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "abc")
		s.writeRead(t, ctx, client, "xyz")
		s.writeRead(t, ctx, client, "foo")
	})

	t.Run("renaming a component should close the old name, and open the new one", func(t *testing.T) {
		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "abc")
		s.writeRead(t, ctx, client, "xyz")
		s.writeRead(t, ctx, client, "foo")

		dbPath := filepath.Join(tmpDir, "db.sqlite")
		dbPathJSON, err := json.Marshal(dbPath)
		require.NoError(t, err)

		comp1 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "abc"},
			Spec: compapi.ComponentSpec{
				Type:    "state.sqlite",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "connectionString", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: dbPathJSON},
					}},
				},
			},
		}
		comp2 := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "bar"},
			Spec: compapi.ComponentSpec{
				Type:    "state.in-memory",
				Version: "v1",
			},
		}

		s.operator.SetComponents(s.operator.Components()[0], comp2, s.operator.Components()[2], s.operator.Components()[3])
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp1, EventType: operatorv1.ResourceEventType_DELETED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp2, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := s.daprd.GetMetaRegisteredComponents(c, ctx)
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{
					Name: "123", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "bar", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
				{
					Name: "xyz", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
				{
					Name: "foo", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			}, resp)
		}, time.Second*5, time.Millisecond*10)

		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "bar")
		s.writeRead(t, ctx, client, "xyz")
		s.writeRead(t, ctx, client, "foo")
	})

	tmpDir = t.TempDir()
	t.Run("deleting a component (through type update) should delete the components", func(t *testing.T) {
		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "bar")
		s.writeRead(t, ctx, client, "xyz")
		s.writeRead(t, ctx, client, "foo")

		secPath := filepath.Join(tmpDir, "foo")
		require.NoError(t, os.WriteFile(secPath, []byte(`{}`), 0o600))
		secPathJSON, err := json.Marshal(secPath)
		require.NoError(t, err)
		component := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "bar"},
			Spec: compapi.ComponentSpec{
				Type:    "secretstores.local.file",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "secretsFile", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: secPathJSON},
					}},
				},
			},
		}
		s.operator.SetComponents(s.operator.Components()[0], component, s.operator.Components()[2], s.operator.Components()[3])
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &component, EventType: operatorv1.ResourceEventType_UPDATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := s.daprd.GetMetaRegisteredComponents(c, ctx)
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{Name: "bar", Type: "secretstores.local.file", Version: "v1"},
				{
					Name: "123", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "xyz", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
				{
					Name: "foo", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			}, resp)
		}, time.Second*5, time.Millisecond*10)

		s.writeRead(t, ctx, client, "123")
		s.writeExpectError(t, ctx, client, "bar", http.StatusBadRequest)
		s.writeRead(t, ctx, client, "xyz")
		s.writeRead(t, ctx, client, "foo")
	})

	t.Run("deleting all components should result in no components remaining", func(t *testing.T) {
		s.writeRead(t, ctx, client, "123")
		s.writeExpectError(t, ctx, client, "bar", http.StatusBadRequest)
		s.writeRead(t, ctx, client, "xyz")
		s.writeRead(t, ctx, client, "foo")

		comp1 := s.operator.Components()[0]
		comp2 := s.operator.Components()[2]
		comp3 := s.operator.Components()[3]
		s.operator.SetComponents(s.operator.Components()[1])
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp1, EventType: operatorv1.ResourceEventType_DELETED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp2, EventType: operatorv1.ResourceEventType_DELETED})
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp3, EventType: operatorv1.ResourceEventType_DELETED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := s.daprd.GetMetaRegisteredComponents(c, ctx)
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{Name: "bar", Type: "secretstores.local.file", Version: "v1"},
			}, resp)
		}, time.Second*10, time.Millisecond*10)

		s.writeExpectError(t, ctx, client, "123", http.StatusInternalServerError)
		s.writeExpectError(t, ctx, client, "bar", http.StatusInternalServerError)
		s.writeExpectError(t, ctx, client, "xyz", http.StatusInternalServerError)
		s.writeExpectError(t, ctx, client, "foo", http.StatusInternalServerError)
	})

	t.Run("recreate state component should make it available again", func(t *testing.T) {
		comp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "123"},
			Spec: compapi.ComponentSpec{
				Type:    "state.in-memory",
				Version: "v1",
			},
		}
		s.operator.AddComponents(comp)
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, s.daprd.GetMetaRegisteredComponents(t, ctx), 2)
		}, time.Second*5, time.Millisecond*10)
		s.writeRead(t, ctx, client, "123")
	})
}

func (s *state) writeExpectError(t *testing.T, ctx context.Context, client *http.Client, compName string, expCode int) {
	t.Helper()

	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s", s.daprd.HTTPPort(), compName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, nil)
	require.NoError(t, err)
	s.doReq(t, client, req, expCode, fmt.Sprintf(
		`\{"errorCode":"(ERR_STATE_STORE_NOT_CONFIGURED|ERR_STATE_STORE_NOT_FOUND)","message":"state store %s (is not configured|is not found)","details":\[.*\]\}`,
		compName))
}

func (s *state) writeRead(t *testing.T, ctx context.Context, client *http.Client, compName string) {
	t.Helper()

	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s", s.daprd.HTTPPort(), url.QueryEscape(compName))
	getURL := postURL + "/foo"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL,
		strings.NewReader(`[{"key": "foo", "value": "bar"}]`))
	require.NoError(t, err)
	s.doReq(t, client, req, http.StatusNoContent, "")

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	require.NoError(t, err)
	s.doReq(t, client, req, http.StatusOK, `"bar"`)
}

func (s *state) doReq(t *testing.T, client *http.Client, req *http.Request, expCode int, expBody string) {
	t.Helper()

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, expCode, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Regexp(t, expBody, string(body))
}
