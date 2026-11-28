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

package statestore

import (
	"context"
	"strings"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(kubernetesmode))
}

// kubernetesmode ensures that spec.workflow.stateStore works in Kubernetes
// mode: the operator serves a Configuration resource carrying the field, and
// daprd keeps the workflow state in the bound store and none in the default
// actor state store.
type kubernetesmode struct {
	daprd     *daprd.Daprd
	oper      *operator.Operator
	place     *placement.Placement
	sched     *scheduler.Scheduler
	defaultDB *sqlite.SQLite
	wfDB      *sqlite.SQLite
}

func (k *kubernetesmode) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	k.defaultDB = sqlite.New(t,
		sqlite.WithName("default-store"),
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	k.wfDB = sqlite.New(t,
		sqlite.WithName("wallet-workflows"),
		sqlite.WithCreateStateTables(),
	)

	kubeapi := kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sen.Port(),
		),
		kubernetes.WithDaprPods(t, "default", map[string]string{"myapp": "workflowstatestore"}),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			Items: []configapi.Configuration{{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "workflowstatestore"},
				Spec: configapi.ConfigurationSpec{
					MTLSSpec: &configapi.MTLSSpec{
						ControlPlaneTrustDomain: "integration.test.dapr.io",
						SentryAddress:           sen.Address(),
					},
					WorkflowSpec: &configapi.WorkflowSpec{
						StateStore: "wallet-workflows",
					},
				},
			}},
		}),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			Items: []compapi.Component{
				k.defaultDB.GetComponentResource(t, "default"),
				k.wfDB.GetComponentResource(t, "default"),
			},
		}),
	)

	k.oper = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	k.place = placement.New(t, placement.WithSentry(t, sen))
	k.sched = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithKubeconfig(kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		// The scheduler ID must match the TLS cert DNS names issued by
		// Sentry.
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	k.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("workflowstatestore"),
		daprd.WithAppID("myapp"),
		daprd.WithNamespace("default"),
		daprd.WithSentry(t, sen),
		daprd.WithControlPlaneAddress(k.oper.Address()),
		daprd.WithPlacementAddresses(k.place.Address()),
		daprd.WithSchedulerAddresses(k.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	)

	return []framework.Option{
		framework.WithProcesses(k.defaultDB, k.wfDB, sen, kubeapi, k.oper, k.sched, k.place, k.daprd),
	}
}

func (k *kubernetesmode) Run(t *testing.T, ctx context.Context) {
	k.oper.WaitUntilRunning(t, ctx)
	k.sched.WaitUntilRunning(t, ctx)
	k.place.WaitUntilRunning(t, ctx)
	k.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("abc").Await(nil)
	}))
	require.NoError(t, reg.AddActivityN("abc", func(dworkflow.ActivityContext) (any, error) {
		return nil, nil
	}))

	client := dworkflow.NewClientWithLogger(k.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "foo")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// The workflow state is in the bound store, under the internal workflow
	// actor types, and is listed from it.
	boundKeys := k.wfDB.StateKeys(t, ctx)
	require.NotEmpty(t, boundKeys)
	for _, key := range boundKeys {
		assert.Contains(t, key, "myapp||dapr.internal.default.myapp.")
	}
	assert.Contains(t, strings.Join(boundKeys, "\n"), "||"+id+"||")

	resp, err := client.ListInstanceIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{id}, resp.InstanceIds)

	// The default actor state store holds no workflow state.
	for _, key := range k.defaultDB.StateKeys(t, ctx) {
		assert.NotContains(t, key, "||dapr.internal.")
	}
}
