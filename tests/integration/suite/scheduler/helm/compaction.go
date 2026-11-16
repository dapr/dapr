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
	suite.Register(new(compaction))
}

// compaction pins the scheduler's etcd compaction defaults. The retention
// bounds how many old revisions etcd keeps, and with it the database size:
// each revision of a deleted job keeps the job's payload until compacted.
type compaction struct {
	helmDefault  *helm.Helm
	helmOverride *helm.Helm
}

func (c *compaction) Setup(t *testing.T) []framework.Option {
	c.helmDefault = helm.New(t,
		helm.WithShowOnlySchedulerSTS(),
	)

	c.helmOverride = helm.New(t,
		helm.WithShowOnlySchedulerSTS(),
		helm.WithValues("dapr_scheduler.etcdCompactionRetention=50000"),
	)

	return []framework.Option{
		framework.WithProcesses(c.helmDefault, c.helmOverride),
	}
}

func (c *compaction) Run(t *testing.T, ctx context.Context) {
	var sts appsv1.StatefulSet

	bs, err := io.ReadAll(c.helmDefault.Stdout(t))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))
	args := sts.Spec.Template.Spec.Containers[0].Args
	assert.Contains(t, args, "--etcd-compaction-mode=revision")
	assert.Contains(t, args, "--etcd-compaction-retention=10000")

	bs, err = io.ReadAll(c.helmOverride.Stdout(t))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(bs, &sts))
	assert.Contains(t, sts.Spec.Template.Spec.Containers[0].Args, "--etcd-compaction-retention=50000")
}
