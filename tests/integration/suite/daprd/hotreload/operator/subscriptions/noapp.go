/*
Copyright 2024 The Dapr Authors
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

package subscriptions

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noapp))
}

type noapp struct {
	daprd *daprd.Daprd
}

func (n *noapp) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	operator := operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)

	n.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	pubsub := compapi.Component{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
		ObjectMeta: metav1.ObjectMeta{Name: "pubsub", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "pubsub.in-memory", Version: "v1"},
	}
	operator.AddComponents(pubsub)

	sub := subapi.Subscription{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "Subscription"},
		ObjectMeta: metav1.ObjectMeta{Name: "sub", Namespace: "default"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "pubsub", Topic: "a",
			Routes: subapi.Routes{Default: "/a"},
		},
	}
	operator.AddSubscriptions(sub)

	return []framework.Option{
		framework.WithProcesses(sentry, operator, n.daprd),
	}
}

func (n *noapp) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	assert.Len(t, n.daprd.GetMetaRegisteredComponents(t, ctx), 1)
	assert.Empty(t, n.daprd.GetMetaSubscriptions(t, ctx))
}
