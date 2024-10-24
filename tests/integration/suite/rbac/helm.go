/*
Copyright 2024 The Dapr Authors
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

package rbac

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(rbac))
}

type rbac struct {
	base              *helm.Helm
	withPriorityClass *helm.Helm
}

func (a *rbac) Setup(t *testing.T) []framework.Option {
	a.base = helm.New(t,
		helm.WithShowOnlyPlacementSTS(),
	)

	a.withPriorityClass = helm.New(t,
		helm.WithShowOnlyPlacementSTS(),
		helm.WithShowOnly("dapr_rbac", "priorityclassresourcequota.yaml"),
		helm.WithGlobalValues("priorityClassName=system-cluster-critical"),
	)

	return []framework.Option{
		framework.WithProcesses(a.base, a.withPriorityClass),
	}
}

func (a *rbac) Run(t *testing.T, ctx context.Context) {
	t.Run("default", func(t *testing.T) {
		sts := helm.UnmarshalStdout[appsv1.StatefulSet](t, a.base)
		require.Len(t, sts, 1)
		assert.Empty(t, sts[0].Spec.Template.Spec.PriorityClassName)
	})
	t.Run("with priority class", func(t *testing.T) {
		var sts appsv1.StatefulSet
		var quotas []v1.ResourceQuota
		a.withPriorityClass.UnmarshalStdoutFunc(t, func(o metav1.PartialObjectMetadata, data []byte) {
			switch o.GetObjectKind().GroupVersionKind().String() {
			case helm.GVKStatefulSet:
				require.NoError(t, yaml.Unmarshal(data, &sts))
			case helm.GVKResourceQuota:
				var q v1.ResourceQuota
				require.NoError(t, yaml.Unmarshal(data, &q))
				quotas = append(quotas, q)
			}
		})
		// sts contains the priority class name "system-cluster-critical"
		assert.NotEmpty(t, sts.Spec.Template.Spec.PriorityClassName)
		assert.Equal(t, "system-cluster-critical", sts.Spec.Template.Spec.PriorityClassName)

		// we expect 2 quotas, as one is new and the other is the deprecated one
		// TODO: remove deprecated quota ("system-critical-quota") in 1.16 version
		require.Len(t, quotas, 2)
		names := []string{"system-critical-quota", "dapr-system-critical-quota"}
		for _, q := range quotas {
			assert.Contains(t, names, q.Name)
		}
		assert.Equal(t, "system-cluster-critical", quotas[0].Spec.ScopeSelector.MatchExpressions[0].Values[0])
		assert.Equal(t, v1.ResourceQuotaScopePriorityClass, quotas[0].Spec.ScopeSelector.MatchExpressions[0].ScopeName)
	})
}
