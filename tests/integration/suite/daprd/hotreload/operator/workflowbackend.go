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
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(workflowbackend))
}

type workflowbackend struct {
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

func (w *workflowbackend) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	w.loglineCreate = logline.New(t, logline.WithStdoutLineContains(
		"Aborting to hot-reload a workflowbackend component which is not supported: wfbackend (workflowbackend.actors/v1)",
	))
	w.loglineUpdate = logline.New(t, logline.WithStdoutLineContains(
		"Aborting to hot-reload a workflowbackend component which is not supported: wfbackend (workflowbackend.sqlite/v1)",
	))
	w.loglineDelete = logline.New(t, logline.WithStdoutLineContains(
		"Aborting to hot-reload a workflowbackend component which is not supported: wfbackend (workflowbackend.actors/v1)",
	))

	w.operatorCreate = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)
	w.operatorUpdate = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)
	w.operatorDelete = operator.New(t,
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

	w.operatorCreate.SetComponents(inmemStore)
	w.operatorUpdate.SetComponents(inmemStore, compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "wfbackend", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "workflowbackend.actors", Version: "v1"},
	})
	w.operatorDelete.SetComponents(inmemStore, compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "wfbackend", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "workflowbackend.actors", Version: "v1"},
	})

	w.daprdCreate = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t,
				"DAPR_TRUST_ANCHORS", string(sentry.CABundle().TrustAnchors),
			),
			exec.WithStdout(w.loglineCreate.Stdout()),
		),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(w.operatorCreate.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)
	w.daprdUpdate = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t,
				"DAPR_TRUST_ANCHORS", string(sentry.CABundle().TrustAnchors),
			),
			exec.WithStdout(w.loglineUpdate.Stdout()),
		),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(w.operatorUpdate.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)
	w.daprdDelete = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t,
				"DAPR_TRUST_ANCHORS", string(sentry.CABundle().TrustAnchors),
			),
			exec.WithStdout(w.loglineDelete.Stdout()),
		),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(w.operatorDelete.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	return []framework.Option{
		framework.WithProcesses(sentry,
			w.operatorCreate, w.operatorUpdate, w.operatorDelete,
			w.loglineCreate, w.loglineUpdate, w.loglineDelete,
			w.daprdCreate, w.daprdUpdate, w.daprdDelete,
		),
	}
}

func (w *workflowbackend) Run(t *testing.T, ctx context.Context) {
	w.daprdCreate.WaitUntilRunning(t, ctx)
	w.daprdUpdate.WaitUntilRunning(t, ctx)
	w.daprdDelete.WaitUntilRunning(t, ctx)

	httpClient := util.HTTPClient(t)

	comps := util.GetMetaComponents(t, ctx, httpClient, w.daprdCreate.HTTPPort())
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{
			Name: "mystore", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
	}, comps)
	actorsComp := compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "wfbackend", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "workflowbackend.actors", Version: "v1"},
	}
	w.operatorCreate.AddComponents(actorsComp)
	w.operatorCreate.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &actorsComp, EventType: operatorv1.ResourceEventType_CREATED})
	w.loglineCreate.EventuallyFoundAll(t)

	comps = util.GetMetaComponents(t, ctx, httpClient, w.daprdUpdate.HTTPPort())
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{Name: "wfbackend", Type: "workflowbackend.actors", Version: "v1"},
		{
			Name: "mystore", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
	}, comps)
	sqliteComp := compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "wfbackend", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "workflowbackend.sqlite", Version: "v1"},
	}
	w.operatorUpdate.SetComponents(sqliteComp)
	w.operatorUpdate.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &sqliteComp, EventType: operatorv1.ResourceEventType_UPDATED})
	w.loglineUpdate.EventuallyFoundAll(t)

	comps = util.GetMetaComponents(t, ctx, httpClient, w.daprdDelete.HTTPPort())
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{Name: "wfbackend", Type: "workflowbackend.actors", Version: "v1"},
		{
			Name: "mystore", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
	}, comps)
	w.operatorDelete.SetComponents()
	w.operatorDelete.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &actorsComp, EventType: operatorv1.ResourceEventType_DELETED})
	w.loglineDelete.EventuallyFoundAll(t)
}
