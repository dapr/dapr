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

package statestores

import (
	"context"
	"io"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(kubernetesmode))
}

// kubernetesmode ensures that spec.actors.stateStores works in Kubernetes
// mode: the operator serves a Configuration resource carrying the bindings,
// and daprd routes the bound actor type's state to its store and the unbound
// type's state to the default actor state store, through the HTTP and gRPC
// APIs.
type kubernetesmode struct {
	daprd     *daprd.Daprd
	defaultDB *sqlite.SQLite
	txDB      *sqlite.SQLite

	boundStore string
	boundType  string
	plainType  string
}

func (k *kubernetesmode) Setup(t *testing.T) []framework.Option {
	k.boundStore = "wallet-transactions"
	k.boundType = "TransferActor"
	k.plainType = "PlainActor"

	sen := sentry.New(t)

	k.defaultDB = sqlite.New(t,
		sqlite.WithName("default-store"),
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	k.txDB = sqlite.New(t,
		sqlite.WithName(k.boundStore),
		sqlite.WithCreateStateTables(),
	)

	cnf := configapi.Configuration{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
		ObjectMeta: metav1.ObjectMeta{Name: "actorstatestores", Namespace: "default"},
		Spec: configapi.ConfigurationSpec{
			NameResolutionSpec: daprd.NameResolutionSpec(t),
			MTLSSpec: &configapi.MTLSSpec{
				ControlPlaneTrustDomain: "localhost",
				SentryAddress:           sen.Address(),
			},
			ActorsSpec: &configapi.ActorsSpec{
				StateStores: []configapi.ActorStateStoreBinding{{
					Name:       k.boundStore,
					ActorTypes: []string{k.boundType},
				}},
			},
		},
	}

	appID := uuid.New().String()

	kubeapi := kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("localhost"),
			"default",
			sen.Port(),
		),
		kubernetes.WithDaprPods(t, "default", map[string]string{appID: cnf.Name}),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ConfigurationList"},
			Items:    []configapi.Configuration{cnf},
		}),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ComponentList"},
			Items: []compapi.Component{
				k.defaultDB.GetComponentResource(t, "default"),
				k.txDB.GetComponentResource(t, "default"),
			},
		}),
	)

	op := operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	place := placement.New(t, placement.WithSentry(t, sen))
	sched := scheduler.New(t,
		scheduler.WithSentry(sen),
		// The scheduler ID must match the TLS cert DNS names issued by
		// Sentry.
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	noop := func(nethttp.ResponseWriter, *nethttp.Request) {}
	actorApp := app.New(t,
		app.WithConfig(`{"entities": ["`+k.boundType+`", "`+k.plainType+`"]}`),
		app.WithHandlerFunc("/actors/"+k.boundType+"/", noop),
		app.WithHandlerFunc("/actors/"+k.plainType+"/", noop),
	)

	k.daprd = daprd.New(t,
		daprd.WithAppID(appID),
		daprd.WithAppPort(actorApp.Port()),
		daprd.WithConfigs(cnf.Name),
		daprd.WithMode("kubernetes"),
		daprd.WithNamespace("default"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sen.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sen.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithControlPlaneAddress(op.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
	)

	return []framework.Option{
		framework.WithProcesses(k.defaultDB, k.txDB, sen, kubeapi, op, place, sched, actorApp, k.daprd),
	}
}

func (k *kubernetesmode) Run(t *testing.T, ctx context.Context) {
	k.daprd.WaitUntilRunning(t, ctx)

	gclient := k.daprd.GRPCClient(t, ctx)
	httpClient := client.HTTP(t)

	saveHTTP := func(t *testing.T, actorType string) {
		t.Helper()
		baseURL := "http://" + k.daprd.HTTPAddress() + "/v1.0/actors/" + actorType + "/h1"
		for _, call := range []struct{ path, body string }{
			{"/method/foo", ""},
			{"/state", `[{"operation":"upsert","request":{"key":"k1","value":"v1"}}]`},
		} {
			req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, baseURL+call.path, strings.NewReader(call.body))
			require.NoError(t, err)
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Lessf(t, resp.StatusCode, 300, "%s: %s", call.path, body)
		}
	}

	saveGRPC := func(t *testing.T, actorType string) {
		t.Helper()
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: actorType, ActorId: "g1", Method: "foo",
		})
		require.NoError(t, err)
		_, err = gclient.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
			ActorType: actorType,
			ActorId:   "g1",
			Operations: []*rtv1.TransactionalActorStateOperation{{
				OperationType: "upsert",
				Key:           "k1",
				Value:         &anypb.Any{Value: []byte("v1")},
			}},
		})
		require.NoError(t, err)
	}

	for _, actorType := range []string{k.boundType, k.plainType} {
		t.Run(actorType+"/http", func(t *testing.T) { saveHTTP(t, actorType) })
		t.Run(actorType+"/grpc", func(t *testing.T) { saveGRPC(t, actorType) })
	}

	appID := k.daprd.AppID()
	assert.Equal(t, []string{
		appID + "||" + k.boundType + "||g1||k1",
		appID + "||" + k.boundType + "||h1||k1",
	}, k.txDB.StateKeys(t, ctx))
	assert.Equal(t, []string{
		appID + "||" + k.plainType + "||g1||k1",
		appID + "||" + k.plainType + "||h1||k1",
	}, k.defaultDB.StateKeys(t, ctx))
}
