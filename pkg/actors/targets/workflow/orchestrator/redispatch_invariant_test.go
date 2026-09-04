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

package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity"
)

// A janitor re-dispatch call that parks on the activity lock and proceeds
// just after the execution completes must land inside the cached-outcome
// window and become a follower, never a fresh execution. The relationship
// lives in two packages; pin it so neither constant drifts silently.
func Test_redispatchCallTimeoutWithinInflightCacheTTL(t *testing.T) {
	require.Less(t, redispatchCallTimeout, activity.InflightCacheTTL)
}
