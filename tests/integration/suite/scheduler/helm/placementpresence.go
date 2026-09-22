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
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(placementpresence))
}

// placementpresence pins the chart contracts the scheduler's placement pod
// informer relies on: the scheduler may watch pods, and placement pods carry
// the label the informer selects on.
type placementpresence struct {
	rbac  *helm.Helm
	place *helm.Helm
}

func (p *placementpresence) Setup(t *testing.T) []framework.Option {
	p.rbac = helm.New(t,
		helm.WithShowOnly("charts/dapr_rbac", "scheduler"),
	)
	p.place = helm.New(t,
		helm.WithShowOnlyPlacementSTS(),
	)

	return []framework.Option{
		framework.WithProcesses(p.rbac, p.place),
	}
}

func (p *placementpresence) Run(t *testing.T, ctx context.Context) {
	bs, err := io.ReadAll(p.rbac.Stdout(t))
	require.NoError(t, err)

	var podRule *rbacv1.PolicyRule
	for doc := range strings.SplitSeq(string(bs), "\n---") {
		var role rbacv1.ClusterRole
		if yaml.Unmarshal([]byte(doc), &role) != nil {
			continue
		}
		if role.Kind != "ClusterRole" || role.Name != "dapr-scheduler" {
			continue
		}
		for i, rule := range role.Rules {
			for _, resource := range rule.Resources {
				if resource == "pods" {
					podRule = &role.Rules[i]
				}
			}
		}
	}
	require.NotNil(t, podRule,
		"the scheduler ClusterRole must cover pods for the placement pod informer")
	for _, verb := range []string{"get", "list", "watch"} {
		assert.Contains(t, podRule.Verbs, verb)
	}

	bs, err = io.ReadAll(p.place.Stdout(t))
	require.NoError(t, err)
	var sts appsv1.StatefulSet
	require.NoError(t, yaml.Unmarshal(bs, &sts))
	assert.Equal(t, "dapr-placement-server", sts.Spec.Template.Labels["app"],
		"the placement pod label is what the scheduler's informer selects on")
}
