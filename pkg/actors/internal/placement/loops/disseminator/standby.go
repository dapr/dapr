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

package disseminator

import (
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
)

// standbyGrace is the least time placement is given to place reported
// standby actor types before they are deemed unsupported. A placement server
// which supports standby hosts always places them: on the type's active hosts
// if there are any, else on its standby hosts, this one included.
const standbyGrace = time.Minute

// testStandbyGrace is a test-only override of the grace period placement is
// given to place the standby actor types, so a test can see the warning of a
// placement server without standby hosts quickly. Not a supported production
// knob.
var testStandbyGrace = sync.OnceValue(func() time.Duration {
	v := os.Getenv("DAPR_ACTORS_TEST_STANDBY_GRACE")
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Warnf("Ignoring invalid DAPR_ACTORS_TEST_STANDBY_GRACE %q", v)
		return 0
	}
	return d
})

// standbyGens issues the generation of each standby check. Process wide, as
// disseminators and their loops are pooled, so a stale check can never match
// a later one.
var standbyGens atomic.Uint64

// watchStandby arms a check that placement places the standby actor types of
// the report just sent. A placement server which predates standby hosts drops
// them from the report and never places them. A round completed after the
// report proves nothing either way, as the server may have computed it before
// the report arrived, so placement is given a grace period instead. Reporting
// the same standby types again keeps the pending check.
func (d *disseminator) watchStandby(types []string) {
	types = slices.Clone(types)
	slices.Sort(types)
	if slices.Equal(types, d.standbyTypes) {
		return
	}

	d.stopStandbyCheck()
	d.standbyTypes = types
	if len(types) == 0 || d.standbyWarned.Load() {
		return
	}

	gen := standbyGens.Add(1)
	d.standbyGen = gen
	loop := d.loop
	grace := max(standbyGrace, 2*d.timeout)
	if g := testStandbyGrace(); g > 0 {
		grace = g
	}
	d.standbyTimer = time.AfterFunc(grace, func() {
		loop.Enqueue(&loops.StandbyCheck{Gen: gen})
	})
}

// handleStandbyCheck warns, once per process, when the reported standby
// actor types are still not placed once the grace period has passed.
func (d *disseminator) handleStandbyCheck(check *loops.StandbyCheck) {
	if check.Gen != d.standbyGen || d.standbyTimer == nil {
		return
	}
	d.standbyTimer = nil

	if d.inflight.HasTables(d.standbyTypes) || !d.standbyWarned.CompareAndSwap(false, true) {
		return
	}

	log.Warnf("The placement server does not support the WorkflowsStandbyHosts preview feature, "+
		"likely an older control plane version: it did not place the standby workflow actor types %v of %s/%s. "+
		"Workflow calls to this app fail or hang while no workflow worker is connected, until the control plane is upgraded",
		d.standbyTypes, d.namespace, d.id)
}

func (d *disseminator) stopStandbyCheck() {
	if d.standbyTimer != nil {
		d.standbyTimer.Stop()
		d.standbyTimer = nil
	}
}
