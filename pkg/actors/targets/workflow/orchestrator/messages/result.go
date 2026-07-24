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

package messages

import (
	"errors"
)

// DispatchResult accumulates per-event failures from dispatching a batch of
// outbound workflow messages or activity invocations.
type DispatchResult struct {
	FailedEventIDs map[int32]struct{}
	Err            error
}

func (r *DispatchResult) RecordFailure(eventID int32, err error) {
	if r.FailedEventIDs == nil {
		r.FailedEventIDs = make(map[int32]struct{})
	}
	r.FailedEventIDs[eventID] = struct{}{}
	r.Err = errors.Join(r.Err, err)
}
