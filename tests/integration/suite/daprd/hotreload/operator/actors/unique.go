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
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contribstate "github.com/dapr/components-contrib/state"
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
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(unique))
}

// unique ensures that daprd shutsdown when a second actor state store is
// added.
type unique struct {
	daprd    *daprd.Daprd
	place    *placement.Placement
	logline  *logline.LogLine
	operator *operator.Operator
}

func (u *unique) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)
	u.operator = operator.New(t,
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
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.URL.Path)
	})
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	u.operator.SetComponents(compapi.Component{
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

	u.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Fatal error from runtime: process component inmem2 error: [INIT_COMPONENT_FAILURE]: initialization error occurred for inmem2 (state.in-memory/v1): detected duplicate actor state store: inmem and inmem2",
		),
	)

	u.place = placement.New(t, placement.WithSentry(sentry))
	u.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(u.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithPlacementAddresses(u.place.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithEnvVars("DAPR_TRUST_ANCHORS", string(sentry.CABundle().TrustAnchors)),
			exec.WithStdout(u.logline.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, srv, u.operator, u.place, u.logline, u.daprd),
	}
}

func (u *unique) Run(t *testing.T, ctx context.Context) {
	u.place.WaitUntilRunning(t, ctx)
	u.daprd.WaitUntilRunning(t, ctx)

	client := u.daprd.GRPCClient(t, ctx)

	require.Len(t, util.GetMetaComponents(t, ctx, util.HTTPClient(t), u.daprd.HTTPPort()), 2)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "helloworld",
		})
		//nolint:testifylint
		if assert.NoError(c, err) {
			assert.Equal(c, "/actors/myactortype/myactorid/method/helloworld", string(resp.GetData()))
		}
	}, time.Second*10, time.Millisecond*100)

	_, err := client.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Operations: []*rtv1.TransactionalActorStateOperation{
			{
				OperationType: string(contribstate.OperationUpsert),
				Key:           "mykey",
				Value:         &anypb.Any{Value: []byte("myvalue")},
			},
		},
	})
	require.NoError(t, err)

	resp, err := client.GetActorState(ctx, &rtv1.GetActorStateRequest{
		ActorType: "myactortype", ActorId: "myactorid", Key: "mykey",
	})
	require.NoError(t, err)
	assert.Equal(t, "myvalue", string(resp.GetData()))

	newComp := compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "inmem2"},
		Spec: compapi.ComponentSpec{
			Type:    "state.in-memory",
			Version: "v1",
			Metadata: []common.NameValuePair{{Name: "actorStateStore", Value: common.DynamicValue{
				JSON: apiextv1.JSON{Raw: []byte("true")},
			}}},
		},
	}

	u.operator.AddComponents(newComp)
	u.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_CREATED})

	assert.Eventually(t, u.logline.FoundAll, time.Second*5, time.Millisecond*100)
}
