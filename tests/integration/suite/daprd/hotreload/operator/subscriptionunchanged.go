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

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
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
	suite.Register(new(subscriptionunchanged))
}

// subscriptionunchanged ensures the backup reconciler does not log at INFO for
// Subscriptions this app uses when nothing has changed. The interval is
// shortened to 1s (the default is 60s) to exercise the reconcile.
type subscriptionunchanged struct {
	daprd    *daprd.Daprd
	daprdLog *log.Log
}

func (s *subscriptionunchanged) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	s.daprdLog = log.New()

	subs := make([][]byte, 0, 2)
	for _, sub := range []subapi.Subscription{
		{
			TypeMeta:   metav1.TypeMeta{Kind: "Subscription", APIVersion: "dapr.io/v2alpha1"},
			ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: "default"},
			Spec: subapi.SubscriptionSpec{
				Pubsubname: "pubsub",
				Topic:      "a",
				Routes:     subapi.Routes{Default: "/a"},
			},
		},
		{
			TypeMeta:   metav1.TypeMeta{Kind: "Subscription", APIVersion: "dapr.io/v2alpha1"},
			ObjectMeta: metav1.ObjectMeta{Name: "rich", Namespace: "default"},
			Spec: subapi.SubscriptionSpec{
				Pubsubname:      "pubsub",
				Topic:           "b",
				Metadata:        map[string]string{"rawPayload": "true"},
				DeadLetterTopic: "dlq",
				BulkSubscribe:   subapi.BulkSubscribe{Enabled: true, MaxMessagesCount: 10},
				Routes: subapi.Routes{
					Rules:   []subapi.Rule{{Match: `event.type == "x"`, Path: "/x"}},
					Default: "/b",
				},
			},
			Scopes: []string{"myapp"},
		},
	} {
		b, err := json.Marshal(sub)
		require.NoError(t, err)
		subs = append(subs, b)
	}

	op := operator.New(t,
		operator.WithSentry(sentry),
		operator.WithListSubscriptionsV2Fn(func(context.Context, *operatorv1.ListSubscriptionsRequest) (*operatorv1.ListSubscriptionsResponse, error) {
			return &operatorv1.ListSubscriptionsResponse{Subscriptions: subs}, nil
		}),
	)
	op.AddComponents(compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "pubsub", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "pubsub.in-memory", Version: "v1"},
	})

	s.daprd = daprd.New(t,
		daprd.WithAppID("myapp"),
		daprd.WithMode("kubernetes"),
		daprd.WithLogLevel("debug"),
		daprd.WithHotReloadReconcileInterval(time.Second),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(op.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors)),
			exec.WithStdout(s.daprdLog),
			exec.WithStderr(s.daprdLog),
		),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, op, s.daprd),
	}
}

func (s *subscriptionunchanged) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, s.daprd.GetMetaSubscriptions(c, ctx), 2)
	}, 10*time.Second, 10*time.Millisecond)

	// Confirm the backup reconcile actually runs in the window (interval is 1s),
	// so the assert.Never below is meaningful and not vacuous.
	require.Eventually(t, func() bool {
		return s.daprdLog.Contains("Running scheduled Subscription reconcile")
	}, 10*time.Second, 10*time.Millisecond, "backup reconcile did not run")

	assert.Never(t, func() bool {
		return s.daprdLog.Contains("Closing existing Subscription to reload") ||
			s.daprdLog.Contains("Adding Subscription for processing") ||
			s.daprdLog.Contains("Subscription updated:")
	}, 5*time.Second, 100*time.Millisecond,
		"unchanged Subscription was reloaded by the backup reconciler")
}
