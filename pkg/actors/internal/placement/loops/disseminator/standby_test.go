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
	"bytes"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

const standbyWarning = "does not support the WorkflowsStandbyHosts preview feature"

// captureLog returns the disseminator's log output for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stdout) })
	return &buf
}

func v1Tables(types ...string) *v1pb.PlacementTables {
	t := &v1pb.PlacementTables{Entries: make(map[string]*v1pb.PlacementTable)}
	for _, actorType := range types {
		t.Entries[actorType] = &v1pb.PlacementTable{
			LoadMap: map[string]*v1pb.Host{
				"10.0.0.1:50002": {Name: "10.0.0.1:50002", Id: "app"},
			},
		}
	}
	return t
}

// v1Round runs a full v1 dissemination round installing a table for each of
// the given actor types.
func v1Round(t *testing.T, diss *disseminator, version uint64, types ...string) {
	t.Helper()
	if diss.roundChangedTypes == nil {
		diss.roundChangedTypes = make(map[string]struct{})
	}
	for _, order := range []*loops.Order{
		{Op: loops.OrderLock, Version: version},
		{Op: loops.OrderUpdate, Version: version, V1Tables: v1Tables(types...)},
		{Op: loops.OrderUnlock, Version: version},
	} {
		require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{Order: order}))
	}
}

func reportStandby(diss *disseminator, types ...string) {
	diss.handleReportHost(&loops.ReportHost{Report: &loops.Report{
		AppID:             "app",
		ActorTypes:        []string{"myactor"},
		StandbyActorTypes: types,
	}})
}

// fireStandbyCheck delivers the pending standby check as its timer would.
func fireStandbyCheck(t *testing.T, diss *disseminator) {
	t.Helper()
	require.NotNil(t, diss.standbyTimer, "expected a pending standby check")
	diss.handleStandbyCheck(&loops.StandbyCheck{Gen: diss.standbyGen})
	assert.Nil(t, diss.standbyTimer)
}

func TestStandbyCheck(t *testing.T) {
	t.Run("warns once the grace passes without the standby types placed", func(t *testing.T) {
		buf := captureLog(t)
		diss, _, _ := newTestDisseminator(t)

		reportStandby(diss, "wf", "act")
		t.Cleanup(diss.stopStandbyCheck)

		// A round after the report placed only the active type.
		v1Round(t, diss, 1, "myactor")
		fireStandbyCheck(t, diss)

		assert.True(t, diss.standbyWarned.Load())
		assert.Equal(t, 1, strings.Count(buf.String(), standbyWarning))
		assert.Contains(t, buf.String(), "[act wf]")
	})

	t.Run("does not warn when the table includes the standby types", func(t *testing.T) {
		buf := captureLog(t)
		diss, _, _ := newTestDisseminator(t)

		reportStandby(diss, "wf", "act")
		t.Cleanup(diss.stopStandbyCheck)

		// A round computed before the report arrived lacks the standby types,
		// the next one places them.
		v1Round(t, diss, 1, "myactor")
		v1Round(t, diss, 2, "myactor", "wf", "act")
		fireStandbyCheck(t, diss)

		assert.False(t, diss.standbyWarned.Load())
		assert.NotContains(t, buf.String(), standbyWarning)
	})

	t.Run("does not warn when the v2 tables include the standby types", func(t *testing.T) {
		buf := captureLog(t)
		diss := newTestDisseminatorV2(t)
		collectAcks(diss)

		reportStandby(diss, "wf")
		t.Cleanup(diss.stopStandbyCheck)

		for _, order := range []*loops.Order{
			{Op: loops.OrderLock, Version: 1, Scope: []string{"wf"}, Partial: true},
			{
				Op: loops.OrderUpdate, Version: 1, Partial: true,
				Versions: map[string]uint64{"wf": 1},
				V2Tables: v2Tables(map[string][]string{"wf": {"a:1"}}),
			},
			{Op: loops.OrderUnlock, Version: 1, Scope: []string{"wf"}, Partial: true},
		} {
			require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{Order: order}))
		}
		fireStandbyCheck(t, diss)

		assert.False(t, diss.standbyWarned.Load())
		assert.NotContains(t, buf.String(), standbyWarning)
	})

	t.Run("does not warn twice", func(t *testing.T) {
		buf := captureLog(t)
		warned := new(atomic.Bool)

		diss, _, _ := newTestDisseminator(t)
		diss.standbyWarned = warned
		reportStandby(diss, "wf")
		fireStandbyCheck(t, diss)
		require.True(t, warned.Load())

		// A later standby registration on the same stream arms no check.
		reportStandby(diss)
		reportStandby(diss, "wf")
		assert.Nil(t, diss.standbyTimer)

		// Nor does a reconnect, which starts a new disseminator.
		diss2, _, _ := newTestDisseminator(t)
		diss2.standbyWarned = warned
		reportStandby(diss2, "wf")
		assert.Nil(t, diss2.standbyTimer)
		diss2.handleStandbyCheck(&loops.StandbyCheck{Gen: diss2.standbyGen})

		assert.Equal(t, 1, strings.Count(buf.String(), standbyWarning))
	})

	t.Run("a superseded check is ignored", func(t *testing.T) {
		buf := captureLog(t)
		diss, _, _ := newTestDisseminator(t)

		reportStandby(diss, "wf")
		stale := diss.standbyGen

		// The worker connected: the types are no longer standby.
		reportStandby(diss)
		assert.Nil(t, diss.standbyTimer)
		diss.handleStandbyCheck(&loops.StandbyCheck{Gen: stale})

		// Reporting the same standby types again keeps the pending check.
		reportStandby(diss, "wf")
		t.Cleanup(diss.stopStandbyCheck)
		gen := diss.standbyGen
		reportStandby(diss, "wf")
		assert.Equal(t, gen, diss.standbyGen)
		diss.handleStandbyCheck(&loops.StandbyCheck{Gen: stale})
		assert.NotNil(t, diss.standbyTimer)

		assert.False(t, diss.standbyWarned.Load())
		assert.NotContains(t, buf.String(), standbyWarning)
	})
}
