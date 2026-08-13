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

package loops

import (
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// IsTransientLeaderError reports whether err is the expected "not a leader"
// rejection from a placement non-leader replica. Every daprd that round-
// robins onto a non-leader during routine leadership churn or placement chaos
// sees this. Treating it as a noteworthy error spams the runtime log with
// hundreds of identical lines per second across the deployment; callers use
// this predicate to downgrade those lines to debug.
func IsTransientLeaderError(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	return st.Code() == codes.FailedPrecondition && strings.Contains(st.Message(), "is not a leader")
}
