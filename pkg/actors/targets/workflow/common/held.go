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

package common

import (
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/kit/ptr"
)

// Held operations (the WorkflowsHeldOperations preview). While no daprd of an
// app hosts its workflow actor type, a start or a new event aimed at it is
// held as a one-shot scheduler reminder on the workflow actor, and the first
// host to come up replays it.
//
// Neither name may start with a prefix the orchestrator already dispatches
// on: "start", "new-event", "timer-", "activity-result-", "cascade-terminate"
// or "parent-notify".
const (
	// ReminderNameHeldStart is the one held start of an instance. The
	// reminder is keyed by the instance ID, so retries of the same start,
	// which each mint a new execution ID, collapse onto it.
	ReminderNameHeldStart = "held-start"

	// ReminderPrefixHeldEvent prefixes each held event of an instance; a
	// unique suffix keeps distinct events apart.
	ReminderPrefixHeldEvent = "held-event-"
)

const (
	defaultHeldOperationTimeout = time.Hour

	// heldOperationRetryInterval and heldOperationMaxRetries bound how long a
	// replay that fails keeps being retried once a host runs it. It covers a
	// held event delivered before the held start of its instance: scheduler
	// jobs are partitioned by name, so the two can arrive in either order.
	heldOperationRetryInterval = time.Second
	heldOperationMaxRetries    = 60
)

// HeldOperationTimeout bounds how long an operation may be held. go-cron
// rejects a TTL on a one-shot job and never expires a staged one, so the
// bound is checked by the replaying host against the time the operation was
// requested. The env override exists for integration tests; it is not a
// supported production knob.
var HeldOperationTimeout = sync.OnceValue(func() time.Duration {
	return EnvDurationOr("DAPR_WORKFLOW_HELD_OPERATION_TIMEOUT", defaultHeldOperationTimeout)
})

// HeldOperationExpired reports whether an operation requested at requested
// has been held for longer than HeldOperationTimeout.
func HeldOperationExpired(requested time.Time) bool {
	return time.Since(requested) > HeldOperationTimeout()
}

// HeldOperationPolicy is the failure policy of a held operation's reminder:
// a bounded number of retries, unlike the retry-forever policy of the
// reminders a host creates for an instance it already owns.
func HeldOperationPolicy() *commonv1pb.JobFailurePolicy {
	return &commonv1pb.JobFailurePolicy{
		Policy: &commonv1pb.JobFailurePolicy_Constant{
			Constant: &commonv1pb.JobFailurePolicyConstant{
				Interval:   durationpb.New(heldOperationRetryInterval),
				MaxRetries: ptr.Of(uint32(heldOperationMaxRetries)),
			},
		},
	}
}
