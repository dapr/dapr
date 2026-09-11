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

//go:build unit

package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/modes"
)

func TestSchedulerPlacementEnabled(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		mode          modes.DaprMode
		schedulerAddr []string
		actorsService string
		exp           bool
	}{
		"kubernetes with actors disabled must not enable from the scheduler address": {
			mode:          modes.KubernetesMode,
			schedulerAddr: []string{"dapr-scheduler:50006"},
			actorsService: "",
			exp:           false,
		},
		"kubernetes with actors enabled": {
			mode:          modes.KubernetesMode,
			schedulerAddr: []string{"dapr-scheduler:50006"},
			actorsService: "placement:dapr-placement:50005",
			exp:           true,
		},
		"self hosted opts in with a scheduler address": {
			mode:          modes.StandaloneMode,
			schedulerAddr: []string{"127.0.0.1:50006"},
			actorsService: "",
			exp:           true,
		},
		"no scheduler address": {
			mode:          modes.StandaloneMode,
			schedulerAddr: nil,
			actorsService: "placement:127.0.0.1:50005",
			exp:           false,
		},
		"blank scheduler address": {
			mode:          modes.StandaloneMode,
			schedulerAddr: []string{""},
			actorsService: "",
			exp:           false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := internalConfig{
				mode:             test.mode,
				schedulerAddress: test.schedulerAddr,
				actorsService:    test.actorsService,
			}
			assert.Equal(t, test.exp, cfg.SchedulerPlacementEnabled())
		})
	}
}
