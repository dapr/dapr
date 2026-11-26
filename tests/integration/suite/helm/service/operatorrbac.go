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

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(operatorrbac))
}

// operatorrbac checks where the operator RBAC lets dapr-operator watch Argo
// Rollouts, with cluster-wide and with namespaced RBAC.
type operatorrbac struct {
	cluster    *helm.Helm
	namespaced *helm.Helm
}

const operatorrbacNS = "dapr-system"

func (o *operatorrbac) Setup(t *testing.T) []framework.Option {
	o.cluster = helm.New(t,
		helm.WithNamespace(operatorrbacNS),
		helm.WithValues("global.argoRolloutServiceReconciler.enabled=true"),
		helm.WithShowOnly("charts/dapr_rbac", "operator"),
	)
	o.namespaced = helm.New(t,
		helm.WithNamespace(operatorrbacNS),
		helm.WithValues(
			"global.rbac.namespaced=true",
			"global.argoRolloutServiceReconciler.enabled=true",
		),
		helm.WithShowOnly("charts/dapr_rbac", "operator"),
	)

	return []framework.Option{
		framework.WithProcesses(o.cluster, o.namespaced),
	}
}

func (o *operatorrbac) Run(t *testing.T, ctx context.Context) {
	t.Run("cluster RBAC grants rollouts cluster-wide", func(t *testing.T) {
		assert.Contains(t, helm.WatchScopes(t, o.cluster, operatorrbacNS, "dapr-operator", "rollouts"), "*")
	})

	t.Run("namespaced RBAC grants rollouts in the release namespace only", func(t *testing.T) {
		assert.Equal(t, []string{operatorrbacNS}, helm.WatchScopes(t, o.namespaced, operatorrbacNS, "dapr-operator", "rollouts"))
	})
}
