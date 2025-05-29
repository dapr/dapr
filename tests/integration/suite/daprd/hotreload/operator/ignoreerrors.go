/*
Copyright 2024 The Dapr Authors
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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ignoreerrors))
}

type ignoreerrors struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
	logline  *logline.LogLine
}

func (i *ignoreerrors) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	i.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)

	i.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Failed to init component a (state.sqlite/v1): [INIT_COMPONENT_FAILURE]: initialization error occurred for a (state.sqlite/v1): missing connection string",
			"Ignoring error processing component: process component a error: [INIT_COMPONENT_FAILURE]: initialization error occurred for a (state.sqlite/v1): [INIT_COMPONENT_FAILURE]: initialization error occurred for a (state.sqlite/v1): missing connection string",
			"Error processing component, daprd will exit gracefully: process component a error: [INIT_COMPONENT_FAILURE]: initialization error occurred for a (state.sqlite/v1): [INIT_COMPONENT_FAILURE]: initialization error occurred for a (state.sqlite/v1): missing connection string",
		),
	)

	i.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(i.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithExit1(),
		daprd.WithLogLineStdout(i.logline),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, i.operator, i.logline, i.daprd),
	}
}

func (i *ignoreerrors) Run(t *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(t, ctx)

	assert.Empty(t, i.daprd.GetMetaRegisteredComponents(t, ctx))

	t.Run("adding a component should become available", func(t *testing.T) {
		newComp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "a"},
			Spec:       compapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
		}
		i.operator.SetComponents(newComp)
		i.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Len(t, i.daprd.GetMetaRegisteredComponents(t, ctx), 1)
		}, time.Second*5, time.Millisecond*10)
	})

	t.Run("Updating a component with an error should be closed and ignored if `ignoreErrors=true`", func(t *testing.T) {
		upComp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "a"},
			Spec:       compapi.ComponentSpec{Type: "state.sqlite", Version: "v1", IgnoreErrors: true},
		}
		i.operator.SetComponents(upComp)
		i.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &upComp, EventType: operatorv1.ResourceEventType_UPDATED})
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Empty(t, i.daprd.GetMetaRegisteredComponents(t, ctx))
		}, time.Second*5, time.Millisecond*10)
	})

	t.Run("Updating a `ignoreErrors=true` component should make it available again", func(t *testing.T) {
		newComp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "a"},
			Spec:       compapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
		}
		i.operator.SetComponents(newComp)
		i.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_UPDATED})
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Len(t, i.daprd.GetMetaRegisteredComponents(t, ctx), 1)
		}, time.Second*5, time.Millisecond*10)
	})

	t.Run("Updating a component with an error should be closed and exit 1 if `ignoreErrors=false`", func(t *testing.T) {
		upComp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "a"},
			Spec:       compapi.ComponentSpec{Type: "state.sqlite", Version: "v1", IgnoreErrors: false},
		}
		i.operator.SetComponents(upComp)
		i.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &upComp, EventType: operatorv1.ResourceEventType_UPDATED})
		i.logline.EventuallyFoundAll(t)
	})
}
