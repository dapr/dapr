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
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(fsgroup))
}

// fsgroup verifies that the Scheduler StatefulSet renders a pod fsGroup by
// default, so the non-root process can write to its block volume, and that the
// fsGroup can be removed for platforms that assign one themselves.
type fsgroup struct {
	byDefault *helm.Helm
	unset     *helm.Helm
}

func (f *fsgroup) Setup(t *testing.T) []framework.Option {
	f.byDefault = helm.New(t,
		helm.WithShowOnlySchedulerSTS(),
	)
	f.unset = helm.New(t,
		helm.WithShowOnlySchedulerSTS(),
		helm.WithValues("dapr_scheduler.securityContext.fsGroup=null"),
	)

	return []framework.Option{
		framework.WithProcesses(f.byDefault, f.unset),
	}
}

func (f *fsgroup) Run(t *testing.T, ctx context.Context) {
	sts := helm.UnmarshalStdout[appsv1.StatefulSet](t, f.byDefault)
	require.Len(t, sts, 1)
	require.NotNil(t, sts[0].Spec.Template.Spec.SecurityContext)
	require.NotNil(t, sts[0].Spec.Template.Spec.SecurityContext.FSGroup)
	require.Equal(t, int64(65532), *sts[0].Spec.Template.Spec.SecurityContext.FSGroup)

	sts = helm.UnmarshalStdout[appsv1.StatefulSet](t, f.unset)
	require.Len(t, sts, 1)
	if sc := sts[0].Spec.Template.Spec.SecurityContext; sc != nil {
		require.Nil(t, sc.FSGroup)
	}
}
