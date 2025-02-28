/*
Copyright 2025 The Dapr Authors
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

package idler

import (
	"time"

	"github.com/dapr/dapr/pkg/actors/targets"
)

// idler is a wrapper for an actor which should be idled in some time in the
// future.
type idler struct {
	targets.Interface
	idleTime time.Time
}

func New(actor targets.Interface, idleIn time.Duration) targets.Idlable {
	return &idler{
		Interface: actor,
		idleTime:  time.Now().Add(idleIn),
	}
}

func (i *idler) ScheduledTime() time.Time {
	return i.idleTime
}
