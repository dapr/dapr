/*
Copyright 2023 The Dapr Authors
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

package api

import (
	"context"
	"strconv"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	resapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	operator "github.com/dapr/dapr/tests/integration/framework/process/operator"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(authz))
}

// authz tests the authz of the operator API which is based on client request
// namespace.
type authz struct {
	sentry  *procsentry.Sentry
	kubeapi *kubernetes.Kubernetes
	op      *operator.Operator
}

func (a *authz) Setup(t *testing.T) []framework.Option {
	a.sentry = procsentry.New(t, procsentry.WithTrustDomain("integration.test.dapr.io"))

	a.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			a.sentry.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{Items: []configapi.Configuration{
			{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
			},
		}}),
		kubernetes.WithClusterDaprResiliencyList(t, &resapi.ResiliencyList{Items: []resapi.Resiliency{
			{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Resiliency"},
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
			},
		}}),
		kubernetes.WithClusterDaprSubscriptionList(t, &subapi.SubscriptionList{Items: []subapi.Subscription{
			{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Subscription"},
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
			},
		}}),
	)

	a.op = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(a.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(a.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(a.kubeapi, a.sentry, a.op),
	}
}

func (a *authz) Run(t *testing.T, ctx context.Context) {
	a.sentry.WaitUntilRunning(t, ctx)
	a.op.WaitUntilRunning(t, ctx)

	t.Run("no client auth should error", func(t *testing.T) {
		conn, err := grpc.DialContext(ctx,
			"localhost:"+strconv.Itoa(a.op.Port()),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, conn.Close())
		})
		client := operatorv1pb.NewOperatorClient(conn)
		resp, err := client.ListComponents(ctx, &operatorv1pb.ListComponentsRequest{
			Namespace: "default",
		})
		require.Error(t, err)
		assert.Nil(t, resp)
	})

	client := a.op.Dial(t, ctx, "default", a.sentry)

	type tcase struct {
		funcGoodNamespace func() (any, error)
		funcBadNamespace  func() (any, error)
	}

	for name, test := range map[string]tcase{
		"ComponentUpdate": {
			funcGoodNamespace: func() (any, error) {
				stream, err := client.ComponentUpdate(ctx, &operatorv1pb.ComponentUpdateRequest{Namespace: "default"})
				require.NoError(t, err)
				a.kubeapi.Informer().Add(t, &compapi.Component{
					TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
					ObjectMeta: metav1.ObjectMeta{Name: "mycomponent", Namespace: "default"},
				})
				return stream.Recv()
			},
			funcBadNamespace: func() (any, error) {
				stream, err := client.ComponentUpdate(ctx, &operatorv1pb.ComponentUpdateRequest{Namespace: "random-namespace"})
				require.NoError(t, err)
				return stream.Recv()
			},
		},
		"GetConfiguration": {
			funcGoodNamespace: func() (any, error) {
				resp, err := client.GetConfiguration(ctx, &operatorv1pb.GetConfigurationRequest{Namespace: "default", Name: "foo"})
				return resp, err
			},
			funcBadNamespace: func() (any, error) {
				resp, err := client.GetConfiguration(ctx, &operatorv1pb.GetConfigurationRequest{Namespace: "random-namespace", Name: "foo"})
				return resp, err
			},
		},
		"GetResiliency": {
			funcGoodNamespace: func() (any, error) {
				return client.GetResiliency(ctx, &operatorv1pb.GetResiliencyRequest{Namespace: "default", Name: "foo"})
			},
			funcBadNamespace: func() (any, error) {
				return client.GetResiliency(ctx, &operatorv1pb.GetResiliencyRequest{Namespace: "random-namespace", Name: "foo"})
			},
		},
		"HTTPEndpointUpdate": {
			funcGoodNamespace: func() (any, error) {
				stream, err := client.HTTPEndpointUpdate(ctx, &operatorv1pb.HTTPEndpointUpdateRequest{Namespace: "default"})
				require.NoError(t, err)
				a.kubeapi.Informer().Add(t, &httpendapi.HTTPEndpoint{
					TypeMeta:   metav1.TypeMeta{Kind: "HTTPEndpoint", APIVersion: "dapr.io/v1alpha1"},
					ObjectMeta: metav1.ObjectMeta{Name: "myendpoint", Namespace: "default"},
				})
				return stream.Recv()
			},
			funcBadNamespace: func() (any, error) {
				stream, err := client.HTTPEndpointUpdate(ctx, &operatorv1pb.HTTPEndpointUpdateRequest{Namespace: "random-namespace"})
				require.NoError(t, err)
				return stream.Recv()
			},
		},
		"ListComponents": {
			funcGoodNamespace: func() (any, error) {
				return client.ListComponents(ctx, &operatorv1pb.ListComponentsRequest{Namespace: "default"})
			},
			funcBadNamespace: func() (any, error) {
				return client.ListComponents(ctx, &operatorv1pb.ListComponentsRequest{Namespace: "random-namespace"})
			},
		},
		"ListHTTPEndpoints": {
			funcGoodNamespace: func() (any, error) {
				return client.ListHTTPEndpoints(ctx, &operatorv1pb.ListHTTPEndpointsRequest{Namespace: "default"})
			},
			funcBadNamespace: func() (any, error) {
				return client.ListHTTPEndpoints(ctx, &operatorv1pb.ListHTTPEndpointsRequest{Namespace: "random-namespace"})
			},
		},
		"ListResiliency": {
			funcGoodNamespace: func() (any, error) {
				return client.ListResiliency(ctx, &operatorv1pb.ListResiliencyRequest{Namespace: "default"})
			},
			funcBadNamespace: func() (any, error) {
				return client.ListResiliency(ctx, &operatorv1pb.ListResiliencyRequest{Namespace: "random-namespace"})
			},
		},
		"ListSubscriptions": {
			funcGoodNamespace: func() (any, error) {
				// ListSubscriptions is not implemented in the operator.
				return 1, nil
			},
			funcBadNamespace: func() (any, error) {
				return client.ListSubscriptions(ctx, new(emptypb.Empty))
			},
		},
		"ListSubscriptionsV2": {
			funcGoodNamespace: func() (any, error) {
				return client.ListSubscriptionsV2(ctx, &operatorv1pb.ListSubscriptionsRequest{Namespace: "default"})
			},
			funcBadNamespace: func() (any, error) {
				return client.ListSubscriptionsV2(ctx, &operatorv1pb.ListSubscriptionsRequest{Namespace: "random-namespace"})
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Bad namespace should error permission denied
			resp, err := test.funcBadNamespace()
			s, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.PermissionDenied.String(), s.Code().String())
			assert.Contains(t, s.Message(), "identity does not match requested namespace")
			assert.Nil(t, resp)

			// Good namespace should not error
			resp, err = test.funcGoodNamespace()
			require.NoError(t, err)
			assert.NotNil(t, resp)
		})
	}
}
