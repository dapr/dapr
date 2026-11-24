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
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(controlplanerbac))
}

// controlplanerbac checks that the chart renders cluster RBAC only for the
// parts of the control plane that are on.
type controlplanerbac struct {
	defaults         *helm.Helm
	injectorOff      *helm.Helm
	reconcilerOff    *helm.Helm
	watchdogOn       *helm.Helm
	namespacedRollup *helm.Helm
}

type controlplanerbacObject struct {
	Kind     string              `json:"kind"`
	Metadata metav1.ObjectMeta   `json:"metadata"`
	Rules    []rbacv1.PolicyRule `json:"rules"`
}

func (c *controlplanerbac) Setup(t *testing.T) []framework.Option {
	c.defaults = helm.New(t)
	c.injectorOff = helm.New(t, helm.WithValues("dapr_sidecar_injector.enabled=false"))
	c.reconcilerOff = helm.New(t, helm.WithValues("dapr_operator.serviceReconciler.enabled=false"))
	c.watchdogOn = helm.New(t, helm.WithValues(
		"dapr_operator.watchInterval=1m",
		"global.operator.watchdogCanPatchPodLabels=true",
	))
	c.namespacedRollup = helm.New(t, helm.WithValues(
		"global.rbac.namespaced=true",
		"global.argoRolloutServiceReconciler.enabled=true",
	))

	return []framework.Option{
		framework.WithProcesses(c.defaults, c.injectorOff, c.reconcilerOff, c.watchdogOn, c.namespacedRollup),
	}
}

func (c *controlplanerbac) Run(t *testing.T, ctx context.Context) {
	t.Run("defaults", func(t *testing.T) {
		objs := helm.UnmarshalStdout[controlplanerbacObject](t, c.defaults)
		assert.True(t, controlplanerbacHas(objs, "ClusterRole", "dapr-injector"))
		assert.True(t, controlplanerbacHas(objs, "ClusterRoleBinding", "dapr-injector"))
		assert.Equal(t, "rbac", controlplanerbacGet(t, objs, "ClusterRole", "dapr-injector").Metadata.Labels["app.kubernetes.io/component"])
		assert.Equal(t, "rbac", controlplanerbacGet(t, objs, "ClusterRole", "dapr-operator-admin").Metadata.Labels["app.kubernetes.io/component"])

		// Placement makes no Kubernetes API calls.
		assert.False(t, controlplanerbacHas(objs, "ClusterRole", "dapr-placement"))
		assert.False(t, controlplanerbacHas(objs, "ClusterRoleBinding", "dapr-placement"))

		admin := controlplanerbacGet(t, objs, "ClusterRole", "dapr-operator-admin")
		// The watchdog is off by default, so nothing deletes pods.
		assert.Equal(t, []string{"get", "list", "watch"}, controlplanerbacVerbs(admin, "", "pods"))
		assert.Contains(t, controlplanerbacVerbs(admin, "", "services"), "create")
		assert.Contains(t, controlplanerbacVerbs(admin, "apps", "deployments/finalizers"), "update")
	})

	t.Run("injector off renders no injector cluster RBAC", func(t *testing.T) {
		objs := helm.UnmarshalStdout[controlplanerbacObject](t, c.injectorOff)
		assert.False(t, controlplanerbacHas(objs, "ClusterRole", "dapr-injector"))
		assert.False(t, controlplanerbacHas(objs, "ClusterRoleBinding", "dapr-injector"))
		assert.True(t, controlplanerbacHas(objs, "ClusterRole", "dapr-operator-admin"))
	})

	t.Run("service reconciler off renders no Service or finalizer writes", func(t *testing.T) {
		admin := controlplanerbacGet(t, helm.UnmarshalStdout[controlplanerbacObject](t, c.reconcilerOff), "ClusterRole", "dapr-operator-admin")
		assert.Equal(t, []string{"get", "list", "watch"}, controlplanerbacVerbs(admin, "", "services"))
		assert.Equal(t, []string{"get", "list", "watch"}, controlplanerbacVerbs(admin, "apps", "deployments/finalizers"))
		assert.Equal(t, []string{"get", "list", "watch"}, controlplanerbacVerbs(admin, "apps", "statefulsets/finalizers"))
	})

	t.Run("watchdog on can delete and patch pods", func(t *testing.T) {
		admin := controlplanerbacGet(t, helm.UnmarshalStdout[controlplanerbacObject](t, c.watchdogOn), "ClusterRole", "dapr-operator-admin")
		assert.ElementsMatch(t, []string{"get", "list", "watch", "delete", "patch"}, controlplanerbacVerbs(admin, "", "pods"))
	})

	t.Run("namespaced with argo rollouts renders the rollout rules in the operator Role only", func(t *testing.T) {
		objs := helm.UnmarshalStdout[controlplanerbacObject](t, c.namespacedRollup)
		assert.False(t, controlplanerbacHas(objs, "ClusterRole", "dapr-operator-admin"))
		role := controlplanerbacGet(t, objs, "Role", "dapr-operator")
		assert.Contains(t, controlplanerbacVerbs(role, "argoproj.io", "rollouts"), "delete")
	})
}

func controlplanerbacHas(objs []controlplanerbacObject, kind, name string) bool {
	return slices.ContainsFunc(objs, func(o controlplanerbacObject) bool {
		return o.Kind == kind && o.Metadata.Name == name
	})
}

func controlplanerbacGet(t *testing.T, objs []controlplanerbacObject, kind, name string) controlplanerbacObject {
	t.Helper()
	i := slices.IndexFunc(objs, func(o controlplanerbacObject) bool {
		return o.Kind == kind && o.Metadata.Name == name
	})
	require.GreaterOrEqual(t, i, 0, "%s %s not rendered", kind, name)
	return objs[i]
}

// controlplanerbacVerbs returns every verb the role grants on resource.
func controlplanerbacVerbs(o controlplanerbacObject, group, resource string) []string {
	var verbs []string
	for _, r := range o.Rules {
		if slices.Contains(r.APIGroups, group) && slices.Contains(r.Resources, resource) {
			for _, v := range r.Verbs {
				if !slices.Contains(verbs, v) {
					verbs = append(verbs, v)
				}
			}
		}
	}
	return verbs
}
