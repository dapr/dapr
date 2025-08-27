/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwa.apache.org/licenses/LICENSE-2.0
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

	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(actorstate))
}

type actorstate struct {
	daprdCreate *daprd.Daprd
	daprdUpdate *daprd.Daprd
	daprdDelete *daprd.Daprd

	operatorCreate *operator.Operator
	operatorUpdate *operator.Operator
	operatorDelete *operator.Operator

	loglineCreate *logline.LogLine
	loglineUpdate *logline.LogLine
	loglineDelete *logline.LogLine
}

func (a *actorstate) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	a.loglineCreate = logline.New(t, logline.WithStdoutLineContains(
		"Aborting to hot-reload a state store component that is used as an actor state store: mystore (state.in-memory/v1)",
	))
	a.loglineUpdate = logline.New(t, logline.WithStdoutLineContains(
		"Aborting to hot-reload a state store component that is used as an actor state store: mystore (state.in-memory/v1)",
	))
	a.loglineDelete = logline.New(t, logline.WithStdoutLineContains(
		"Aborting to hot-reload a state store component that is used as an actor state store: mystore (state.in-memory/v1)",
	))

	a.operatorCreate = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)
	a.operatorUpdate = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)
	a.operatorDelete = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)

	inmemStore := compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "mystore", Namespace: "default"},
		Spec: compapi.ComponentSpec{
			Type: "state.in-memory", Version: "v1",
			Metadata: []common.NameValuePair{{Name: "actorStateStore", Value: common.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(`"true"`)}}}},
		},
	}

	a.operatorCreate.SetComponents()
	a.operatorUpdate.SetComponents(inmemStore)
	a.operatorDelete.SetComponents(inmemStore)

	a.daprdCreate = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors)),
			exec.WithStdout(a.loglineCreate.Stdout()),
		),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(a.operatorCreate.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)
	a.daprdUpdate = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors)),
			exec.WithStdout(a.loglineUpdate.Stdout()),
		),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(a.operatorUpdate.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)
	a.daprdDelete = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors)),
			exec.WithStdout(a.loglineDelete.Stdout()),
		),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(a.operatorDelete.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	return []framework.Option{
		framework.WithProcesses(sentry,
			a.operatorCreate, a.operatorUpdate, a.operatorDelete,
			a.loglineCreate, a.loglineUpdate, a.loglineDelete,
			a.daprdCreate, a.daprdUpdate, a.daprdDelete,
		),
	}
}

func (a *actorstate) Run(t *testing.T, ctx context.Context) {
	a.daprdCreate.WaitUntilRunning(t, ctx)
	a.daprdUpdate.WaitUntilRunning(t, ctx)
	a.daprdDelete.WaitUntilRunning(t, ctx)

	comps := a.daprdCreate.GetMetaRegisteredComponents(t, ctx)
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{}, comps)
	inmemStore := compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "mystore", Namespace: "default"},
		Spec: compapi.ComponentSpec{
			Type: "state.in-memory", Version: "v1",
			Metadata: []common.NameValuePair{{Name: "actorStateStore", Value: common.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(`"true"`)}}}},
		},
	}
	a.operatorCreate.AddComponents(inmemStore)
	a.operatorCreate.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &inmemStore, EventType: operatorv1.ResourceEventType_CREATED})
	a.loglineCreate.EventuallyFoundAll(t)

	comps = a.daprdUpdate.GetMetaRegisteredComponents(t, ctx)
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{
			Name: "mystore", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
	}, comps)
	inmemStore.Spec.Metadata = []common.NameValuePair{}
	a.operatorUpdate.SetComponents(inmemStore)
	a.operatorUpdate.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &inmemStore, EventType: operatorv1.ResourceEventType_UPDATED})
	a.loglineUpdate.EventuallyFoundAll(t)

	comps = a.daprdDelete.GetMetaRegisteredComponents(t, ctx)
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{
			Name: "mystore", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
	}, comps)
	a.operatorDelete.SetComponents()
	a.operatorDelete.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &inmemStore, EventType: operatorv1.ResourceEventType_DELETED})
	a.loglineDelete.EventuallyFoundAll(t)
}
