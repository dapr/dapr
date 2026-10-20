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

package helm

import (
	"slices"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type rbacObject struct {
	Kind     string              `json:"kind"`
	Metadata metav1.ObjectMeta   `json:"metadata"`
	Rules    []rbacv1.PolicyRule `json:"rules"`
	Subjects []rbacv1.Subject    `json:"subjects"`
	RoleRef  rbacv1.RoleRef      `json:"roleRef"`
}

// WatchScopes returns where the rendered RBAC lets serviceAccount, in the
// release namespace, list and watch resource: a namespace, or "*" for all
// namespaces.
func WatchScopes(t *testing.T, h *Helm, namespace, serviceAccount, resource string) []string {
	t.Helper()

	objs := UnmarshalStdout[rbacObject](t, h)

	// helm template leaves the namespace off objects without one, which helm
	// install puts in the release namespace.
	nsOf := func(o rbacObject) string {
		if o.Metadata.Namespace == "" {
			return namespace
		}
		return o.Metadata.Namespace
	}

	type roleKey struct{ kind, ns, name string }
	grants := make(map[roleKey]bool)
	for _, o := range objs {
		for _, r := range o.Rules {
			if slices.Contains(r.Resources, resource) &&
				slices.Contains(r.Verbs, "list") && slices.Contains(r.Verbs, "watch") {
				key := roleKey{kind: o.Kind, name: o.Metadata.Name}
				if o.Kind == "Role" {
					key.ns = nsOf(o)
				}
				grants[key] = true
			}
		}
	}

	var scopes []string
	for _, o := range objs {
		if o.Kind != "RoleBinding" && o.Kind != "ClusterRoleBinding" {
			continue
		}
		if !slices.ContainsFunc(o.Subjects, func(s rbacv1.Subject) bool {
			return s.Kind == "ServiceAccount" && s.Name == serviceAccount && s.Namespace == namespace
		}) {
			continue
		}

		key := roleKey{kind: o.RoleRef.Kind, name: o.RoleRef.Name}
		if o.RoleRef.Kind == "Role" {
			key.ns = nsOf(o)
		}
		if !grants[key] {
			continue
		}

		if o.Kind == "ClusterRoleBinding" {
			scopes = append(scopes, "*")
		} else {
			scopes = append(scopes, nsOf(o))
		}
	}

	return scopes
}
