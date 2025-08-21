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

package app

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
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
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(uppercase))
}

type uppercase struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	daprd3 *daprd.Daprd

	operator *operator.Operator
}

func (u *uppercase) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	u.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"nameResolution": {"component": "mdns"}, "features":[{"name":"HotReload","enabled":true}],
					"appHttpPipeline":{"handlers":[{"name":"uppercase","type":"middleware.http.uppercase"},{"name":"uppercase2","type":"middleware.http.uppercase"}]}}}`,
				),
			}, nil
		}),
	)

	handler := nethttp.NewServeMux()
	handler.HandleFunc("/", func(nethttp.ResponseWriter, *nethttp.Request) {})
	handler.HandleFunc("/foo", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, err := io.Copy(w, r.Body)
		assert.NoError(t, err)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	u.operator.SetComponents(compapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "uppercase",
			Namespace: "ns1",
		},
		Spec: compapi.ComponentSpec{
			Type:    "middleware.http.uppercase",
			Version: "v1",
		},
	}, compapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "uppercase2",
			Namespace: "ns1",
		},
		Spec: compapi.ComponentSpec{
			Type:    "middleware.http.routeralias",
			Version: "v1",
			Metadata: []common.NameValuePair{{Name: "routes", Value: common.DynamicValue{
				JSON: apiextv1.JSON{Raw: []byte(`{"/foo":"/v1.0/invoke/nowhere/method/bar"}`)},
			}}},
		},
	})

	u.daprd1 = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(u.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("ns1"),
		daprd.WithAppPort(srv.Port()),
	)
	u.daprd2 = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(u.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("ns2"),
		daprd.WithAppPort(srv.Port()),
	)
	u.daprd3 = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(u.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("ns3"),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(srv, sentry, u.operator, u.daprd1, u.daprd2, u.daprd3),
	}
}

func (u *uppercase) Run(t *testing.T, ctx context.Context) {
	u.daprd1.WaitUntilAppHealth(t, ctx)
	u.daprd2.WaitUntilAppHealth(t, ctx)
	u.daprd3.WaitUntilAppHealth(t, ctx)

	client := client.HTTP(t)
	assert.Len(t, u.daprd1.GetMetaRegisteredComponents(t, ctx), 2)

	t.Run("existing middleware should be loaded", func(t *testing.T) {
		u.doReq(t, ctx, client, u.daprd1, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd2, false)
		u.doReq(t, ctx, client, u.daprd1, u.daprd3, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd2, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd3, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd2, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd3, false)
	})

	t.Run("adding a new middleware should be loaded", func(t *testing.T) {
		newComp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "uppercase",
				Namespace: "ns2",
			},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.uppercase",
				Version: "v1",
			},
		}
		u.operator.AddComponents(newComp)
		u.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_CREATED})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, u.daprd2.GetMetaRegisteredComponents(c, ctx), 1)
		}, time.Second*5, time.Millisecond*10, "expected component to be loaded")

		u.doReq(t, ctx, client, u.daprd1, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd3, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd3, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd3, false)
	})

	t.Run("adding third middleware should be loaded", func(t *testing.T) {
		newComp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "uppercase",
				Namespace: "ns3",
			},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.uppercase",
				Version: "v1",
			},
		}
		u.operator.AddComponents(newComp)
		u.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_CREATED})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, u.daprd3.GetMetaRegisteredComponents(t, ctx), 1)
		}, time.Second*5, time.Millisecond*10, "expected component to be loaded")

		u.doReq(t, ctx, client, u.daprd1, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd3, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd3, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd3, true)
	})

	t.Run("changing the type of middleware should no longer make it available as type needs to match", func(t *testing.T) {
		newComp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "uppercase",
				Namespace: "ns1",
			},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.routeralias",
				Version: "v1",
				Metadata: []common.NameValuePair{{Name: "routes", Value: common.DynamicValue{
					JSON: apiextv1.JSON{Raw: []byte(`{"/foo":"/v1.0/invoke/nowhere/method/bar"}`)},
				}}},
			},
		}
		u.operator.AddComponents(newComp)
		u.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_UPDATED})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := u.daprd1.GetMetaRegisteredComponents(c, ctx)
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{Name: "uppercase", Type: "middleware.http.routeralias", Version: "v1"},
				{Name: "uppercase2", Type: "middleware.http.routeralias", Version: "v1"},
			}, resp)
		}, time.Second*5, time.Millisecond*10, "expected component to be loaded")

		u.doReq(t, ctx, client, u.daprd1, u.daprd1, false)
		u.doReq(t, ctx, client, u.daprd1, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd3, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd1, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd3, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd1, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd3, true)
	})

	t.Run("changing the type of middleware should make it available as type needs to match", func(t *testing.T) {
		newComp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "uppercase2",
				Namespace: "ns1",
			},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.uppercase",
				Version: "v1",
			},
		}
		u.operator.AddComponents(newComp)
		u.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_UPDATED})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := u.daprd1.GetMetaRegisteredComponents(c, ctx)
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{Name: "uppercase", Type: "middleware.http.routeralias", Version: "v1"},
				{Name: "uppercase2", Type: "middleware.http.uppercase", Version: "v1"},
			}, resp)
		}, time.Second*5, time.Millisecond*10, "expected component to be loaded")

		u.doReq(t, ctx, client, u.daprd1, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd3, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd3, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd3, true)
	})

	t.Run("deleting components should no longer make them available", func(t *testing.T) {
		comp1 := &compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "uppercase",
				Namespace: "ns1",
			},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.routeralias",
				Version: "v1",
				Metadata: []common.NameValuePair{{Name: "routes", Value: common.DynamicValue{
					JSON: apiextv1.JSON{Raw: []byte(`{"/foo":"/v1.0/invoke/nowhere/method/bar"}`)},
				}}},
			},
		}
		comp2 := &compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "uppercase2",
				Namespace: "ns1",
			},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.uppercase",
				Version: "v1",
			},
		}
		comp3 := comp2.DeepCopy()
		comp3.ObjectMeta.Name = "uppercase"
		comp3.ObjectMeta.Namespace = "ns2"
		comp4 := comp3.DeepCopy()
		comp4.ObjectMeta.Namespace = "ns3"

		u.operator.SetComponents()
		u.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: comp1, EventType: operatorv1.ResourceEventType_DELETED})
		u.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: comp2, EventType: operatorv1.ResourceEventType_DELETED})
		u.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: comp3, EventType: operatorv1.ResourceEventType_DELETED})
		u.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: comp4, EventType: operatorv1.ResourceEventType_DELETED})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, u.daprd1.GetMetaRegisteredComponents(t, ctx))
			assert.Empty(c, u.daprd2.GetMetaRegisteredComponents(t, ctx))
			assert.Empty(c, u.daprd3.GetMetaRegisteredComponents(t, ctx))
		}, time.Second*5, time.Millisecond*10, "expected component to be loaded")

		u.doReq(t, ctx, client, u.daprd1, u.daprd1, false)
		u.doReq(t, ctx, client, u.daprd1, u.daprd2, false)
		u.doReq(t, ctx, client, u.daprd1, u.daprd3, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd1, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd2, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd3, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd1, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd2, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd3, false)
	})
}

func (u *uppercase) doReq(t require.TestingT, ctx context.Context, client *nethttp.Client, source, target *daprd.Daprd, expectUpper bool) {
	url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/foo", source.HTTPPort(), target.AppID())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader("hello"))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.Do(req)
		if !assert.NoError(c, err) {
			return
		}
		defer resp.Body.Close()
		assert.Equal(c, nethttp.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		assert.NoError(c, err)
		if expectUpper {
			assert.Equal(c, "HELLO", string(body))
		} else {
			assert.Equal(c, "hello", string(body))
		}
	}, time.Second*5, time.Millisecond*10)
}
