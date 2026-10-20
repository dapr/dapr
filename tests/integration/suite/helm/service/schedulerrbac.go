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
	suite.Register(new(schedulerrbac))
}

type schedulerrbac struct {
	cluster    *helm.Helm
	namespaced *helm.Helm
}

const schedulerrbacNS = "dapr-system"

func (s *schedulerrbac) Setup(t *testing.T) []framework.Option {
	s.cluster = helm.New(t,
		helm.WithNamespace(schedulerrbacNS),
		helm.WithShowOnly("charts/dapr_rbac", "scheduler"),
	)
	s.namespaced = helm.New(t,
		helm.WithNamespace(schedulerrbacNS),
		helm.WithValues("global.rbac.namespaced=true"),
		helm.WithShowOnly("charts/dapr_rbac", "scheduler"),
	)

	return []framework.Option{
		framework.WithProcesses(s.cluster, s.namespaced),
	}
}

func (s *schedulerrbac) Run(t *testing.T, ctx context.Context) {
	t.Run("default scopes pods to the release namespace", func(t *testing.T) {
		assert.Equal(t, []string{schedulerrbacNS}, helm.WatchScopes(t, s.cluster, schedulerrbacNS, "dapr-scheduler", "pods"))
		assert.Equal(t, []string{"*"}, helm.WatchScopes(t, s.cluster, schedulerrbacNS, "dapr-scheduler", "namespaces"))
	})

	t.Run("namespaced scopes pods to the release namespace", func(t *testing.T) {
		assert.Equal(t, []string{schedulerrbacNS}, helm.WatchScopes(t, s.namespaced, schedulerrbacNS, "dapr-scheduler", "pods"))
	})
}
