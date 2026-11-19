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

package watchhosts

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/healthz"
)

// unreachableReportDelay is how long daprd waits after the first failed
// attempt to reach a Scheduler before it logs why it is not ready. The env
// var exists for integration tests that cannot wait the production delay. It
// is not a supported production knob.
var unreachableReportDelay = sync.OnceValue(func() time.Duration {
	d, err := time.ParseDuration(os.Getenv("DAPR_SCHEDULER_TEST_UNREACHABLE_REPORT_DELAY"))
	if err != nil || d <= 0 {
		return 5 * time.Second
	}
	return d
})

// maxUnreachableReportGap caps the time between two reports.
const maxUnreachableReportGap = 5 * time.Minute

// unreachable tells the user why daprd is not ready while no Scheduler has
// sent a host list since daprd started. Each failed attempt sets the reason on
// the healthz target. The report log line comes after
// unreachableReportDelay, and the gap to the next report doubles each time,
// so the line does not repeat on every retry.
type unreachable struct {
	addrs   string
	htarget healthz.Target

	since time.Time
	next  time.Duration
	gap   time.Duration
	done  bool
}

func newUnreachable(addrs []string, htarget healthz.Target) *unreachable {
	return &unreachable{
		addrs:   strings.Join(addrs, ","),
		htarget: htarget,
	}
}

// failed records a failed attempt to reach a Scheduler.
func (u *unreachable) failed(err error) {
	if u.done {
		return
	}

	// The healthz endpoint needs no API token, so the reason gives only the
	// addresses. The error can hold TLS or DNS details, and only the log line
	// gives it.
	u.htarget.SetNotReadyReason("cannot connect to the Scheduler at " + u.addrs + ", see the daprd log")

	elapsed, ok := u.due(time.Now())
	if !ok {
		return
	}

	log.Errorf("Cannot connect to the Scheduler at %s for %s: %s. "+
		"daprd is not ready until it connects. Workflows, jobs and actor reminders need the Scheduler. "+
		"Start the Scheduler, or set --scheduler-host-address (on Kubernetes, the dapr.io/scheduler-host-address annotation) "+
		"to the address of a running Scheduler. "+
		"See https://docs.dapr.io/concepts/dapr-services/scheduler/#self-hosted-mode",
		u.addrs, elapsed.Round(time.Second), err)
}

// due records a failed attempt at now. It returns the time since the first
// failed attempt, and true when the report is due.
func (u *unreachable) due(now time.Time) (time.Duration, bool) {
	if u.since.IsZero() {
		u.since = now
		u.gap = unreachableReportDelay()
		u.next = u.gap
	}

	elapsed := now.Sub(u.since)
	if elapsed < u.next {
		return elapsed, false
	}

	u.gap = min(2*u.gap, maxUnreachableReportGap)
	u.next = elapsed + u.gap

	return elapsed, true
}

// connected stops the reports after the first host list from a Scheduler.
func (u *unreachable) connected() {
	u.done = true
}
