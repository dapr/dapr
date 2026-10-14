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

package operator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(subscriptionscope))
}

// subscriptionscope is a regression test for the backup reconciler logging a
// Subscription scoped to another app at INFO on every tick. The operator lists
// every Subscription in the namespace, but daprd only stores the ones scoped
// to it, so an out-of-scope Subscription looked new on every reconcile. The
// interval is shortened to 1s (the default is 60s) to exercise the reconcile.
type subscriptionscope struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
	daprdLog *log.Log
}

func (s *subscriptionscope) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	s.daprdLog = log.New()

	sub, err := json.Marshal(subapi.Subscription{
		TypeMeta:   metav1.TypeMeta{Kind: "Subscription", APIVersion: "dapr.io/v2alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "other-app-sub", Namespace: "default"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "pubsub",
			Topic:      "a",
			Routes:     subapi.Routes{Default: "/a"},
		},
		Scopes: []string{"other-app"},
	})
	require.NoError(t, err)

	s.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithListSubscriptionsV2Fn(func(context.Context, *operatorv1.ListSubscriptionsRequest) (*operatorv1.ListSubscriptionsResponse, error) {
			return &operatorv1.ListSubscriptionsResponse{Subscriptions: [][]byte{sub}}, nil
		}),
	)

	s.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithLogLevel("debug"),
		daprd.WithHotReloadReconcileInterval(time.Second),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(s.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors)),
			exec.WithStdout(s.daprdLog),
			exec.WithStderr(s.daprdLog),
		),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, s.operator, s.daprd),
	}
}

func (s *subscriptionscope) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	require.Eventually(t, func() bool {
		return s.daprdLog.Contains("Running scheduled Subscription reconcile")
	}, 10*time.Second, 10*time.Millisecond, "backup reconcile did not run")

	assert.Never(t, func() bool {
		return s.daprdLog.Contains("Adding Subscription for processing: other-app-sub")
	}, 5*time.Second, 100*time.Millisecond,
		"out-of-scope Subscription was processed by the backup reconciler")
	assert.False(t, s.daprdLog.Contains("Subscription updated:"))
}
