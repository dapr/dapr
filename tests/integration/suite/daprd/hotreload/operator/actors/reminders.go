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

package actors

import (
	"context"
	"net/http"
	"sync/atomic"
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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reminders))
}

type reminders struct {
	daprd        *daprd.Daprd
	place        *placement.Placement
	operator     *operator.Operator
	methodcalled atomic.Int64
}

func (r *reminders) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)
	r.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/remindermethod", func(http.ResponseWriter, *http.Request) {
		r.methodcalled.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	r.operator.SetComponents(compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "inmem"},
		Spec: compapi.ComponentSpec{
			Type:    "state.in-memory",
			Version: "v1",
			Metadata: []common.NameValuePair{{Name: "actorStateStore", Value: common.DynamicValue{
				JSON: apiextv1.JSON{Raw: []byte("true")},
			}}},
		},
	})

	r.place = placement.New(t, placement.WithSentry(sentry))
	r.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(r.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithExecOptions(
			exec.WithEnvVars("DAPR_TRUST_ANCHORS", string(sentry.CABundle().TrustAnchors)),
		),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, srv, r.operator, r.place, r.daprd),
	}
}

func (r *reminders) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	client := r.daprd.GRPCClient(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		//nolint:testifylint
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*100)

	_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
		DueTime:   "0ms",
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return r.methodcalled.Load() == 1
	}, time.Second*3, time.Millisecond*100)

	delComp := r.operator.Components()[0]
	r.operator.SetComponents()
	r.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &delComp, EventType: operatorv1.ResourceEventType_DELETED})

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(t, ctx, util.HTTPClient(t), r.daprd.HTTPPort()), 1)
	}, time.Second*5, time.Millisecond*100)
	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
		DueTime:   "0ms",
	})
	require.ErrorContains(t, err, "actors: state store does not exist or incorrectly configured")

	comp := compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "inmem"},
		Spec: compapi.ComponentSpec{
			Type:    "state.in-memory",
			Version: "v1",
		},
	}
	r.operator.AddComponents(comp)
	r.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(t, ctx, util.HTTPClient(t), r.daprd.HTTPPort()), 2)
	}, time.Second*5, time.Millisecond*100)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(t, ctx, util.HTTPClient(t), r.daprd.HTTPPort()), 2)
	}, time.Second*5, time.Millisecond*100)
	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
		DueTime:   "0ms",
	})
	require.ErrorContains(t, err, "actors: state store does not exist or incorrectly configured")

	comp = compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "inmem"},
		Spec: compapi.ComponentSpec{
			Type:    "state.in-memory",
			Version: "v1",
			Metadata: []common.NameValuePair{{Name: "actorStateStore", Value: common.DynamicValue{
				JSON: apiextv1.JSON{Raw: []byte("true")},
			}}},
		},
	}
	r.operator.SetComponents(comp)
	r.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_UPDATED})

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Name:      "remindermethod",
			DueTime:   "0ms",
		})
		//nolint:testifylint
		assert.NoError(c, err)
	}, time.Second*5, time.Millisecond*100)

	assert.Eventually(t, func() bool {
		return r.methodcalled.Load() == 2
	}, time.Second*3, time.Millisecond*100)
}
