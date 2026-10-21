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
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(foreignconfig))
}

// foreignconfig tests that GetConfiguration only serves the configuration
// assigned to the calling app by its pods' dapr.io/config annotation.
type foreignconfig struct {
	sentry   *sentry.Sentry
	operator *operator.Operator
}

func (f *foreignconfig) Setup(t *testing.T) []framework.Option {
	f.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	configType := metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"}
	configs := &configapi.ConfigurationList{
		TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ConfigurationList"},
		Items: []configapi.Configuration{
			{TypeMeta: configType, ObjectMeta: metav1.ObjectMeta{Name: "myconfig", Namespace: "default"}},
			{TypeMeta: configType, ObjectMeta: metav1.ObjectMeta{Name: "otherconfig", Namespace: "default"}},
		},
	}

	kubeapi := kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			f.sentry.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, configs),
		kubernetes.WithDaprPodList(t, "default",
			kubernetes.DaprPod("default", "myapp", "myapp", "myconfig"),
			// Mid rollout that changes the app's configuration.
			kubernetes.DaprPod("default", "rollapp-old", "rollapp", "myconfig"),
			kubernetes.DaprPod("default", "rollapp-new", "rollapp", "otherconfig"),
			// daprd's --config flag is a CSV list and daprd only loads the first.
			kubernetes.DaprPod("default", "csvapp", "csvapp", "myconfig,otherconfig"),
		),
	)

	f.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(f.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(f.sentry, kubeapi, f.operator),
	}
}

func (f *foreignconfig) Run(t *testing.T, ctx context.Context) {
	f.sentry.WaitUntilRunning(t, ctx)
	f.operator.WaitUntilRunning(t, ctx)

	get := func(appID, config string) error {
		_, err := f.operator.Dial(t, ctx, f.sentry, appID).GetConfiguration(ctx, &operatorv1.GetConfigurationRequest{
			Name:      config,
			Namespace: "default",
		})
		return err
	}

	t.Run("app gets its assigned configuration", func(t *testing.T) {
		require.NoError(t, get("myapp", "myconfig"))
	})

	t.Run("app cannot get a configuration assigned to another app", func(t *testing.T) {
		assert.Equal(t, codes.PermissionDenied, status.Code(get("myapp", "otherconfig")))
	})

	t.Run("app without a pod cannot get any configuration", func(t *testing.T) {
		assert.Equal(t, codes.PermissionDenied, status.Code(get("noapp", "myconfig")))
	})

	t.Run("app mid rollout gets the configuration of either pod", func(t *testing.T) {
		require.NoError(t, get("rollapp", "myconfig"))
		require.NoError(t, get("rollapp", "otherconfig"))
	})

	t.Run("comma separated annotation assigns only the first configuration", func(t *testing.T) {
		require.NoError(t, get("csvapp", "myconfig"))
		assert.Equal(t, codes.PermissionDenied, status.Code(get("csvapp", "otherconfig")))
		assert.Equal(t, codes.PermissionDenied, status.Code(get("csvapp", "myconfig,otherconfig")))
	})
}
