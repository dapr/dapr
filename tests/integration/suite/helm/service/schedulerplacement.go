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
	suite.Register(new(schedulerplacement))
}

// schedulerplacement asserts global.scheduler.placement.enabled is the one
// switch: it moves the scheduler's --placement-enabled flag and the placement
// StatefulSet together, so the chart can never deploy two placement
// authorities.
type schedulerplacement struct {
	schedulerOff *helm.Helm
	schedulerOn  *helm.Helm
	chartOn      *helm.Helm
	chartOff     *helm.Helm
	chartNoSched *helm.Helm
}

func (s *schedulerplacement) Setup(t *testing.T) []framework.Option {
	s.schedulerOff = helm.New(t,
		helm.WithShowOnlySchedulerSTS(),
	)
	s.schedulerOn = helm.New(t,
		helm.WithValues("global.scheduler.placement.enabled=true"),
		helm.WithShowOnlySchedulerSTS(),
	)
	s.chartOn = helm.New(t,
		helm.WithValues("global.scheduler.placement.enabled=true"),
	)
	s.chartOff = helm.New(t)
	s.chartNoSched = helm.New(t,
		helm.WithValues("global.scheduler.placement.enabled=true", "global.scheduler.enabled=false"),
		helm.WithExpectFailure(),
	)

	return []framework.Option{
		framework.WithProcesses(s.schedulerOff, s.schedulerOn, s.chartOn, s.chartOff, s.chartNoSched),
	}
}

func (s *schedulerplacement) Run(t *testing.T, ctx context.Context) {
	schedulerArgs := func(t *testing.T, h *helm.Helm) []string {
		t.Helper()
		var sts appsv1.StatefulSet
		bs, err := io.ReadAll(h.Stdout(t))
		require.NoError(t, err)
		require.NoError(t, yaml.Unmarshal(bs, &sts))
		require.Len(t, sts.Spec.Template.Spec.Containers, 1)
		return sts.Spec.Template.Spec.Containers[0].Args
	}

	hasArg := func(args []string, want string) bool {
		for _, arg := range args {
			if strings.TrimSpace(arg) == want {
				return true
			}
		}
		return false
	}

	t.Run("default has placement disabled on the scheduler", func(t *testing.T) {
		args := schedulerArgs(t, s.schedulerOff)
		assert.True(t, hasArg(args, "--placement-enabled=false"), "scheduler args: %v", args)
	})

	t.Run("enabling the value enables it on the scheduler", func(t *testing.T) {
		args := schedulerArgs(t, s.schedulerOn)
		assert.True(t, hasArg(args, "--placement-enabled=true"), "scheduler args: %v", args)
	})

	t.Run("default deploys the placement statefulset", func(t *testing.T) {
		bs, err := io.ReadAll(s.chartOff.Stdout(t))
		require.NoError(t, err)
		assert.Contains(t, string(bs), "dapr_placement_statefulset")
	})

	t.Run("enabling the value undeploys the placement statefulset", func(t *testing.T) {
		bs, err := io.ReadAll(s.chartOn.Stdout(t))
		require.NoError(t, err)
		assert.NotContains(t, string(bs), "dapr_placement_statefulset")
	})

	t.Run("the value without a scheduler is rejected", func(t *testing.T) {
		// No service would serve actor placement.
		bs, err := io.ReadAll(s.chartNoSched.Stderr(t))
		require.NoError(t, err)
		assert.Contains(t, string(bs), "global.scheduler.placement.enabled requires global.scheduler.enabled")
	})

	t.Run("placement derives the scheduler address itself", func(t *testing.T) {
		// placement derives the address in kubernetes mode, the chart passes
		// nothing.
		bs, err := io.ReadAll(s.chartOff.Stdout(t))
		require.NoError(t, err)
		assert.NotContains(t, string(bs), "--scheduler-address")
	})
}
