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
	"math/rand/v2"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dapr/dapr/pkg/backoff"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
)

const (
	// RetryBackoffBase and RetryBackoffCap bound the jittered retry
	// backoffs on the workflow drive, reminder and create paths.
	RetryBackoffBase = 50 * time.Millisecond
	RetryBackoffCap  = 2 * time.Second
)

// RejectedTurnBackoffCap bounds the extra hold a workflow actor puts on the
// retry of a turn that daprd rejects again and again. A turn that is rejected
// once or twice usually converges on the next try, so the hold starts at
// RetryBackoffBase and doubles. A turn that is still rejected after about ten
// tries does not converge by itself (for example, the workflow code is not
// deterministic), so retrying it more than twice a minute only costs a full
// state load and an app round trip each time.
const RejectedTurnBackoffCap = 30 * time.Second

// rejectedTurnBackoffBase is the first hold of RejectedTurnBackoff. Integration
// tests shorten or lengthen it through the environment.
var rejectedTurnBackoffBase = sync.OnceValue(func() time.Duration {
	return EnvDurationOr("DAPR_WORKFLOW_TEST_REJECTED_TURN_BACKOFF_BASE", RetryBackoffBase)
})

// RejectedTurnBackoff returns how long to hold the retry of a turn that daprd
// has now rejected the given number of times in a row: the base doubled for
// each rejection after the first, up to RejectedTurnBackoffCap. The workflow
// actor adds this hold to the interval the scheduler waits between retries,
// because that interval is constant for the life of the reminder.
func RejectedTurnBackoff(rejections int) time.Duration {
	if rejections < 1 {
		return 0
	}
	base := rejectedTurnBackoffBase()
	// Doubling more than this overflows or passes the cap for any base.
	const maxShift = 30
	shift := min(rejections-1, maxShift)
	if base > RejectedTurnBackoffCap>>shift {
		return RejectedTurnBackoffCap
	}
	return base << shift
}

// JitterBackoff is the decorrelated exponential jitter from pkg/backoff,
// aliased here for the workflow-internal call sites.
type JitterBackoff = backoff.Jitter

func NewJitterBackoff(base, cap time.Duration) *JitterBackoff {
	return backoff.NewJitter(base, cap)
}

// RetryForeverPolicy returns the failure policy for one-shot workflow
// reminders: retry forever at a jittered constant interval drawn from
// [RetryBackoffBase, RetryBackoffCap). The scheduler protocol can only express
// a constant per-job retry interval, so the decorrelation happens across jobs:
// each create draws its own interval instead of the whole fleet retrying in a
// synchronized 1s lockstep.
func RetryForeverPolicy() *commonv1pb.JobFailurePolicy {
	interval := RetryBackoffBase + rand.N(RetryBackoffCap-RetryBackoffBase) //nolint:gosec // retry jitter, not security sensitive
	return &commonv1pb.JobFailurePolicy{
		Policy: &commonv1pb.JobFailurePolicy_Constant{
			Constant: &commonv1pb.JobFailurePolicyConstant{
				Interval:   durationpb.New(interval),
				MaxRetries: nil,
			},
		},
	}
}
