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

package getconfiguration

import (
	"context"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(daprdcsv))
}

// daprdcsv tests that a daprd started as the injector starts it, with the
// dapr.io/config annotation passed verbatim to --config, starts and loads the
// first configuration when the annotation lists several.
type daprdcsv struct {
	daprd *daprd.Daprd
}

func (d *daprdcsv) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	config := func(name, feature string) configapi.Configuration {
		return configapi.Configuration{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: configapi.ConfigurationSpec{
				MTLSSpec: &configapi.MTLSSpec{
					ControlPlaneTrustDomain: "integration.test.dapr.io",
					SentryAddress:           sen.Address(),
				},
				Features: []configapi.FeatureSpec{{Name: feature, Enabled: new(true)}},
			},
		}
	}

	kubeapi := kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sen.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ConfigurationList"},
			Items: []configapi.Configuration{
				config("myconfig", "MyConfigLoaded"),
				config("otherconfig", "OtherConfigLoaded"),
			},
		}),
		kubernetes.WithDaprPods(t, "default", map[string]string{"myapp": "myconfig,otherconfig"}),
	)

	op := operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	d.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("myconfig,otherconfig"),
		daprd.WithSentryAddress(sen.Address()),
		daprd.WithControlPlaneAddress(op.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithAppID("myapp"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sen.CABundle().X509.TrustAnchors),
		)),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	)

	return []framework.Option{
		framework.WithProcesses(sen, kubeapi, op, d.daprd),
	}
}

func (d *daprdcsv) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	features := d.daprd.GetMetaEnabledFeatures(t, ctx)
	assert.Contains(t, features, "MyConfigLoaded")
	assert.NotContains(t, features, "OtherConfigLoaded")
}
