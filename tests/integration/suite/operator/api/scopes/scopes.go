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

package scopes

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	mcpapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	resapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(scopes))
}

// scopes tests that the operator only returns resources scoped to the calling
// app ID, and never resolves secrets of resources scoped to other apps.
type scopes struct {
	sentry   *sentry.Sentry
	operator *operator.Operator
}

var (
	otherSecret = base64.StdEncoding.EncodeToString([]byte("other-only"))
	secretRef   = []commonapi.NameValuePair{{
		Name:         "Authorization",
		SecretKeyRef: commonapi.SecretKeyRef{Name: "other-token", Key: "token"},
	}}
)

func (s *scopes) Setup(t *testing.T) []framework.Option {
	s.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	objMeta := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: "default"}
	}

	secret := corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: objMeta("other-token"),
		Data:       map[string][]byte{"token": []byte("other-only")},
	}

	endpointType := metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "HTTPEndpoint"}
	endpointSpec := httpendapi.HTTPEndpointSpec{BaseURL: "http://localhost:1234"}
	endpoints := &httpendapi.HTTPEndpointList{
		TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "HTTPEndpointList"},
		Items: []httpendapi.HTTPEndpoint{
			{TypeMeta: endpointType, ObjectMeta: objMeta("unscoped"), Spec: endpointSpec},
			{TypeMeta: endpointType, ObjectMeta: objMeta("scoped-myapp"), Spec: endpointSpec, Scoped: commonapi.Scoped{Scopes: []string{"myapp"}}},
			{
				TypeMeta:   endpointType,
				ObjectMeta: objMeta("scoped-other"),
				Spec:       httpendapi.HTTPEndpointSpec{BaseURL: "http://localhost:1234", Headers: secretRef},
				Scoped:     commonapi.Scoped{Scopes: []string{"otherapp"}},
			},
		},
	}

	mcpType := metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "MCPServer"}
	mcpSpec := mcpapi.MCPServerSpec{Endpoint: mcpapi.MCPEndpoint{
		StreamableHTTP: &mcpapi.MCPStreamableHTTP{URL: "http://localhost:1234/mcp"},
	}}
	mcpServers := &mcpapi.MCPServerList{
		TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "MCPServerList"},
		Items: []mcpapi.MCPServer{
			{TypeMeta: mcpType, ObjectMeta: objMeta("unscoped"), Spec: mcpSpec},
			{TypeMeta: mcpType, ObjectMeta: objMeta("scoped-myapp"), Spec: mcpSpec, Scoped: commonapi.Scoped{Scopes: []string{"myapp"}}},
			{
				TypeMeta:   mcpType,
				ObjectMeta: objMeta("scoped-other"),
				Spec: mcpapi.MCPServerSpec{Endpoint: mcpapi.MCPEndpoint{
					StreamableHTTP: &mcpapi.MCPStreamableHTTP{URL: "http://localhost:1234/mcp", Headers: secretRef},
				}},
				Scoped: commonapi.Scoped{Scopes: []string{"otherapp"}},
			},
		},
	}

	subType := metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "Subscription"}
	subSpec := subapi.SubscriptionSpec{Pubsubname: "pubsub", Topic: "topic", Routes: subapi.Routes{Default: "/"}}
	subs := &subapi.SubscriptionList{
		TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "SubscriptionList"},
		Items: []subapi.Subscription{
			{TypeMeta: subType, ObjectMeta: objMeta("unscoped"), Spec: subSpec},
			{TypeMeta: subType, ObjectMeta: objMeta("scoped-myapp"), Spec: subSpec, Scopes: []string{"myapp"}},
			{TypeMeta: subType, ObjectMeta: objMeta("scoped-other"), Spec: subSpec, Scopes: []string{"otherapp"}},
		},
	}

	resType := metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Resiliency"}
	resiliencies := &resapi.ResiliencyList{
		TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ResiliencyList"},
		Items: []resapi.Resiliency{
			{TypeMeta: resType, ObjectMeta: objMeta("unscoped")},
			{TypeMeta: resType, ObjectMeta: objMeta("scoped-myapp"), Scopes: []string{"myapp"}},
			{TypeMeta: resType, ObjectMeta: objMeta("scoped-other"), Scopes: []string{"otherapp"}},
		},
	}

	policies := store.New(metav1.GroupVersionKind{Group: "dapr.io", Version: "v1alpha1", Kind: "WorkflowAccessPolicy"})
	policyType := metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"}
	policies.Add(
		&wfaclapi.WorkflowAccessPolicy{TypeMeta: policyType, ObjectMeta: objMeta("unscoped")},
		&wfaclapi.WorkflowAccessPolicy{TypeMeta: policyType, ObjectMeta: objMeta("scoped-myapp"), Scoped: commonapi.Scoped{Scopes: []string{"myapp"}}},
		&wfaclapi.WorkflowAccessPolicy{TypeMeta: policyType, ObjectMeta: objMeta("scoped-other"), Scoped: commonapi.Scoped{Scopes: []string{"otherapp"}}},
	)

	kubeapi := kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			s.sentry.Port(),
		),
		kubernetes.WithClusterDaprHTTPEndpointList(t, endpoints),
		kubernetes.WithClusterDaprMCPServerList(t, mcpServers),
		kubernetes.WithClusterDaprSubscriptionListV2(t, subs),
		kubernetes.WithClusterDaprResiliencyList(t, resiliencies),
		kubernetes.WithClusterDaprWorkflowAccessPolicyListFromStore(t, policies),
		kubernetes.WithSecretList(t, &corev1.SecretList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "SecretList"},
			Items:    []corev1.Secret{secret},
		}),
		kubernetes.WithSecretGet(t, &secret),
	)

	s.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(s.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(s.sentry, kubeapi, s.operator),
	}
}

func (s *scopes) Run(t *testing.T, ctx context.Context) {
	s.sentry.WaitUntilRunning(t, ctx)
	s.operator.WaitUntilRunning(t, ctx)

	client := s.operator.Dial(t, ctx, s.sentry, "myapp")

	t.Run("list http endpoints", func(t *testing.T) {
		items := operator.AssertListNames(t, []string{"unscoped", "scoped-myapp"}, func() ([][]byte, error) {
			resp, err := client.ListHTTPEndpoints(ctx, &operatorv1.ListHTTPEndpointsRequest{Namespace: "default"})
			return resp.GetHttpEndpoints(), err
		})
		assert.NotContains(t, string(bytes.Join(items, nil)), otherSecret)
	})

	t.Run("list mcp servers", func(t *testing.T) {
		items := operator.AssertListNames(t, []string{"unscoped", "scoped-myapp"}, func() ([][]byte, error) {
			resp, err := client.ListMCPServers(ctx, &operatorv1.ListMCPServersRequest{Namespace: "default"})
			return resp.GetMcpServers(), err
		})
		assert.NotContains(t, string(bytes.Join(items, nil)), otherSecret)
	})

	t.Run("list subscriptions", func(t *testing.T) {
		operator.AssertListNames(t, []string{"unscoped", "scoped-myapp"}, func() ([][]byte, error) {
			resp, err := client.ListSubscriptionsV2(ctx, &operatorv1.ListSubscriptionsRequest{Namespace: "default"})
			return resp.GetSubscriptions(), err
		})
	})

	t.Run("list resiliencies", func(t *testing.T) {
		operator.AssertListNames(t, []string{"unscoped", "scoped-myapp"}, func() ([][]byte, error) {
			resp, err := client.ListResiliency(ctx, &operatorv1.ListResiliencyRequest{Namespace: "default"})
			return resp.GetResiliencies(), err
		})
	})

	t.Run("list workflow access policies", func(t *testing.T) {
		operator.AssertListNames(t, []string{"unscoped", "scoped-myapp"}, func() ([][]byte, error) {
			resp, err := client.ListWorkflowAccessPolicy(ctx, &operatorv1.ListWorkflowAccessPolicyRequest{Namespace: "default"})
			return resp.GetPolicies(), err
		})
	})

	t.Run("get resiliency", func(t *testing.T) {
		_, err := client.GetResiliency(ctx, &operatorv1.GetResiliencyRequest{Namespace: "default", Name: "scoped-myapp"})
		require.NoError(t, err)

		// A resiliency scoped to another app must be indistinguishable from a
		// missing one.
		_, err = client.GetResiliency(ctx, &operatorv1.GetResiliencyRequest{Namespace: "default", Name: "scoped-other"})
		assert.Equal(t, codes.NotFound, status.Code(err))
		_, errMissing := client.GetResiliency(ctx, &operatorv1.GetResiliencyRequest{Namespace: "default", Name: "missing"})
		assert.Equal(t, codes.NotFound, status.Code(errMissing))
		assert.Equal(t, strings.ReplaceAll(errMissing.Error(), "missing", "scoped-other"), err.Error())
	})

	t.Run("scoped app receives its resources with resolved secrets", func(t *testing.T) {
		other := s.operator.Dial(t, ctx, s.sentry, "otherapp")
		items := operator.AssertListNames(t, []string{"unscoped", "scoped-other"}, func() ([][]byte, error) {
			resp, err := other.ListHTTPEndpoints(ctx, &operatorv1.ListHTTPEndpointsRequest{Namespace: "default"})
			return resp.GetHttpEndpoints(), err
		})
		assert.Contains(t, string(bytes.Join(items, nil)), otherSecret)

		items = operator.AssertListNames(t, []string{"unscoped", "scoped-other"}, func() ([][]byte, error) {
			resp, err := other.ListMCPServers(ctx, &operatorv1.ListMCPServersRequest{Namespace: "default"})
			return resp.GetMcpServers(), err
		})
		assert.Contains(t, string(bytes.Join(items, nil)), otherSecret)
	})
}
