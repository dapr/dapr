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
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(routeralias))
}

type routeralias struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd

	operator *operator.Operator
}

func (r *routeralias) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	r.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"nameResolution": {"component": "mdns"}, "features":[{"name":"HotReload","enabled":true}],
					"appHttpPipeline":{"handlers":[{"name":"routeralias1","type":"middleware.http.routeralias"},{"name":"routeralias2","type":"middleware.http.routeralias"},{"name":"routeralias3","type":"middleware.http.routeralias"}]}}}`,
				),
			}, nil
		}),
	)

	r.operator.SetComponents(compapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "routeralias1",
			Namespace: "ns1",
		},
		Spec: compapi.ComponentSpec{
			Type:    "middleware.http.routeralias",
			Version: "v1",
			Metadata: []common.NameValuePair{{Name: "routes", Value: common.DynamicValue{
				JSON: apiextv1.JSON{Raw: []byte(`{"/helloworld":"/foobar","/xyz":"/abc"}`)},
			}}},
		},
	}, compapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "routeralias2",
			Namespace: "ns1",
		},
		Spec: compapi.ComponentSpec{
			Type:    "middleware.http.routeralias",
			Version: "v1",
			Metadata: []common.NameValuePair{{Name: "routes", Value: common.DynamicValue{
				JSON: apiextv1.JSON{Raw: []byte(`{"/helloworld":"/xyz","/foobar":"/abc"}`)},
			}}},
		},
	})

	srv := func(id string) *prochttp.HTTP {
		handler := nethttp.NewServeMux()
		handler.HandleFunc("/", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			fmt.Fprintf(w, "%s:%s", id, r.URL.Path)
		})
		return prochttp.New(t, prochttp.WithHandler(handler))
	}
	srv1 := srv("daprd1")
	srv2 := srv("daprd2")

	r.daprd1 = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(r.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("ns2"),
		daprd.WithAppPort(srv1.Port()),
	)
	r.daprd2 = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(r.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("ns1"),
		daprd.WithAppPort(srv2.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, srv1, srv2, r.operator, r.daprd1, r.daprd2),
	}
}

func (r *routeralias) Run(t *testing.T, ctx context.Context) {
	r.daprd1.WaitUntilAppHealth(t, ctx)
	r.daprd2.WaitUntilAppHealth(t, ctx)

	client := client.HTTP(t)
	r.doReq(t, ctx, client, fmt.Sprintf("/v1.0/invoke/%s/method/helloworld", r.daprd2.AppID()), "daprd2:/abc")

	comp1 := compapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "routeralias1",
			Namespace: "ns1",
		},
		Spec: compapi.ComponentSpec{
			Type:    "middleware.http.routeralias",
			Version: "v1",
			Metadata: []common.NameValuePair{{Name: "routes", Value: common.DynamicValue{
				JSON: apiextv1.JSON{Raw: []byte(`{"/abc":"/barfoo","/xyz":"/abc"}`)},
			}}},
		},
	}
	comp2 := compapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "routeralias2",
			Namespace: "ns1",
		},
		Spec: compapi.ComponentSpec{
			Type:    "middleware.http.routeralias",
			Version: "v1",
			Metadata: []common.NameValuePair{{Name: "routes", Value: common.DynamicValue{
				JSON: apiextv1.JSON{Raw: []byte(`{"/helloworld":"/xyz","/foobar":"/abc"}`)},
			}}},
		},
	}
	comp3 := compapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "routeralias3",
			Namespace: "ns1",
		},
		Spec: compapi.ComponentSpec{
			Type:    "middleware.http.routeralias",
			Version: "v1",
			Metadata: []common.NameValuePair{{Name: "routes", Value: common.DynamicValue{
				JSON: apiextv1.JSON{Raw: []byte(`{"/helloworld":"/eee","/foobar":"/fff","/xyz":"/aaa"}`)},
			}}},
		},
	}
	r.operator.SetComponents(comp1, comp2, comp3)
	r.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp1, EventType: operatorv1.ResourceEventType_UPDATED})
	r.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp2, EventType: operatorv1.ResourceEventType_UPDATED})
	r.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp3, EventType: operatorv1.ResourceEventType_CREATED})

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		r.doReq(c, ctx, client, fmt.Sprintf("/v1.0/invoke/%s/method/helloworld", r.daprd2.AppID()), "daprd2:/aaa")
	}, time.Second*10, time.Millisecond*10)
}

func (r *routeralias) doReq(t require.TestingT, ctx context.Context, client *nethttp.Client, path, expect string) {
	url := fmt.Sprintf("http://localhost:%d/%s", r.daprd1.HTTPPort(), path)
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, nethttp.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, expect, string(body))
}
