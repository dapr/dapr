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
	"net/http"
	"strconv"
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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(middleware))
}

type middleware struct {
	daprd    *daprd.Daprd
	operator *operator.Operator

	respCh chan int
}

func (m *middleware) Setup(t *testing.T) []framework.Option {
	m.respCh = make(chan int, 1)
	newHTTPServer := func(i int) *prochttp.HTTP {
		handler := http.NewServeMux()
		handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		})
		handler.HandleFunc("/helloworld", func(w http.ResponseWriter, r *http.Request) {
			m.respCh <- i
		})
		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	srv1 := newHTTPServer(0)
	srv2 := newHTTPServer(1)
	srv3 := newHTTPServer(2)

	sentry := sentry.New(t)
	m.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}],"httpPipeline":{"handlers":[{"name":"routeralias","type":"middleware.http.routeralias"}]}, "nameResolution": {"component": "mdns"}}}`,
				),
			}, nil
		}),
	)

	m.operator.SetComponents(compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "routeralias", Namespace: "default"},
		Spec: compapi.ComponentSpec{
			Type:    "middleware.http.routeralias",
			Version: "v1",
			Metadata: []common.NameValuePair{
				{Name: "routes", Value: common.DynamicValue{
					JSON: apiextv1.JSON{Raw: []byte(`"{}"`)},
				}},
			},
		},
	})

	m.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars("DAPR_TRUST_ANCHORS", string(sentry.CABundle().TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(m.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithAppPort(srv1.Port()),
		daprd.WithAppID("app1"),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, srv1, srv2, srv3, m.operator, m.daprd),
	}
}

func (m *middleware) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	m.doReq(t, ctx, client, "/v1.0/invoke/app1/method/helloworld", http.StatusOK)
	m.expServerResp(t, ctx, 0)

	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	t.Cleanup(cancel)

	t.Run("expect middleware to be loaded", func(t *testing.T) {
		assert.Len(t, util.GetMetaComponents(t, ctx, client, m.daprd.HTTPPort()), 1)
	})

	t.Run("middleware hot reloading doesn't work yet", func(t *testing.T) {
		m.doReq(t, ctx, client, "/v1.0/invoke/app1/method/helloworld", http.StatusOK)
		m.expServerResp(t, ctx, 0)
		m.doReq(t, ctx, client, "/helloworld", http.StatusNotFound)

		m.operator.SetComponents(compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "routeralias", Namespace: "default"},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.routeralias",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "routes", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"{/helloworld:/v1.0/invoke/app2/method/helloworld}"`)},
					}},
				},
			},
		},
			compapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "state1", Namespace: "default"},
				Spec: compapi.ComponentSpec{
					Type:    "state.in-memory",
					Version: "v1",
				},
			})

		m.operator.ComponentUpdateEvent(t, ctx,
			&api.ComponentUpdateEvent{Component: &m.operator.Components()[0], EventType: operatorv1.ResourceEventType_UPDATED},
		)
		m.operator.ComponentUpdateEvent(t, ctx,
			&api.ComponentUpdateEvent{Component: &m.operator.Components()[1], EventType: operatorv1.ResourceEventType_CREATED},
		)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(t, ctx, client, m.daprd.HTTPPort()), 2)
		}, time.Second*5, time.Millisecond*100, "expected component to be loaded")
		m.doReq(t, ctx, client, "/helloworld", http.StatusNotFound)
	})
}

func (m *middleware) doReq(t require.TestingT, ctx context.Context, client *http.Client, path string, expCode int) {
	reqURL := "http://localhost:" + strconv.Itoa(m.daprd.HTTPPort()) + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, expCode, resp.StatusCode, path)
}

func (m *middleware) expServerResp(t *testing.T, ctx context.Context, server int) {
	select {
	case <-ctx.Done():
		t.Fatal("timed out waiting for response")
	case got := <-m.respCh:
		assert.Equal(t, server, got, "unexpected server response")
	}
}
