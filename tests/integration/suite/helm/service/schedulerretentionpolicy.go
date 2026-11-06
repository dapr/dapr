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
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(schedulerretentionpolicy))
}

type schedulerretentionpolicy struct {
	tests []schedulerRetentionPolicyCase
}

type schedulerRetentionPolicyCase struct {
	name   string
	values []string
	want   *appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy
	chart  *helm.Helm
}

func (s *schedulerretentionpolicy) Setup(t *testing.T) []framework.Option {
	s.tests = []schedulerRetentionPolicyCase{
		{
			name: "default",
		},
		{
			name:   "empty",
			values: []string{`dapr_scheduler.cluster.persistentVolumeClaimRetentionPolicy={}`},
		},
		{
			name:   "null",
			values: []string{`dapr_scheduler.cluster.persistentVolumeClaimRetentionPolicy=null`},
		},
		{
			name:   "retain",
			values: []string{`dapr_scheduler.cluster.persistentVolumeClaimRetentionPolicy={"whenDeleted":"Retain","whenScaled":"Retain"}`},
			want: &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
				WhenDeleted: appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
				WhenScaled:  appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
			},
		},
		{
			name:   "delete",
			values: []string{`dapr_scheduler.cluster.persistentVolumeClaimRetentionPolicy={"whenDeleted":"Delete","whenScaled":"Delete"}`},
			want: &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
				WhenDeleted: appsv1.DeletePersistentVolumeClaimRetentionPolicyType,
				WhenScaled:  appsv1.DeletePersistentVolumeClaimRetentionPolicyType,
			},
		},
		{
			name:   "mixed",
			values: []string{`dapr_scheduler.cluster.persistentVolumeClaimRetentionPolicy={"whenDeleted":"Retain","whenScaled":"Delete"}`},
			want: &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
				WhenDeleted: appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
				WhenScaled:  appsv1.DeletePersistentVolumeClaimRetentionPolicyType,
			},
		},
		{
			name:   "when deleted only",
			values: []string{`dapr_scheduler.cluster.persistentVolumeClaimRetentionPolicy={"whenDeleted":"Delete"}`},
			want: &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
				WhenDeleted: appsv1.DeletePersistentVolumeClaimRetentionPolicyType,
			},
		},
		{
			name:   "when scaled only",
			values: []string{`dapr_scheduler.cluster.persistentVolumeClaimRetentionPolicy={"whenScaled":"Delete"}`},
			want: &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
				WhenScaled: appsv1.DeletePersistentVolumeClaimRetentionPolicyType,
			},
		},
	}

	opts := make([]framework.Option, 0, len(s.tests))
	for i := range s.tests {
		s.tests[i].chart = helm.New(t,
			helm.WithShowOnlySchedulerSTS(),
			helm.WithSetJSON(s.tests[i].values...),
		)
		opts = append(opts, framework.WithProcesses(s.tests[i].chart))
	}
	return opts
}

func (s *schedulerretentionpolicy) Run(t *testing.T, ctx context.Context) {
	base := helm.UnmarshalStdout[appsv1.StatefulSet](t, s.tests[0].chart)
	require.Len(t, base, 1)
	require.Nil(t, base[0].Spec.PersistentVolumeClaimRetentionPolicy)

	for _, tc := range s.tests {
		t.Run(tc.name, func(t *testing.T) {
			sts := helm.UnmarshalStdout[appsv1.StatefulSet](t, tc.chart)
			require.Len(t, sts, 1)
			require.Equal(t, tc.want, sts[0].Spec.PersistentVolumeClaimRetentionPolicy)

			if tc.want == nil {
				rendered, err := io.ReadAll(tc.chart.Stdout(t))
				require.NoError(t, err)
				require.NotContains(t, string(rendered), "persistentVolumeClaimRetentionPolicy:")
			}

			sts[0].Spec.PersistentVolumeClaimRetentionPolicy = nil
			require.Equal(t, base[0], sts[0], "the policy must be the only change to the StatefulSet")
		})
	}
}
