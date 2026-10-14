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
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reusevalues))
}

// reusevalues renders the chart without global.scheduler.placement, as
// `helm upgrade --reuse-values` does from a release installed before 1.19,
// whose values never had the key and are used in place of the new chart
// defaults. Those old values also keep the old global.tag, so the rendered
// scheduler runs a pre-1.19 image: its args must not carry a flag that
// binary does not know.
type reusevalues struct {
	helm *helm.Helm
}

func (r *reusevalues) Setup(t *testing.T) []framework.Option {
	r.helm = helm.New(t,
		helm.WithValues("global.scheduler.placement=null"),
	)

	return []framework.Option{
		framework.WithProcesses(r.helm),
	}
}

func (r *reusevalues) Run(t *testing.T, ctx context.Context) {
	bs, err := io.ReadAll(r.helm.Stdout(t))
	require.NoError(t, err)

	stss := make(map[string]appsv1.StatefulSet)
	for doc := range strings.SplitSeq(string(bs), "\n---") {
		var sts appsv1.StatefulSet
		if yaml.Unmarshal([]byte(doc), &sts) != nil || sts.Kind != "StatefulSet" {
			continue
		}
		stss[sts.Name] = sts
	}

	assert.Contains(t, stss, "dapr-placement-server",
		"placement must still be deployed when the flag is unset")

	require.Contains(t, stss, "dapr-scheduler-server")
	sched := stss["dapr-scheduler-server"]
	require.NotEmpty(t, sched.Spec.Template.Spec.Containers)
	for _, arg := range sched.Spec.Template.Spec.Containers[0].Args {
		assert.NotContains(t, arg, "--placement-enabled",
			"a pre-1.19 scheduler image exits on the unknown flag --placement-enabled")
	}
}
