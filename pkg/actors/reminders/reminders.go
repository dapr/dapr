/*
Copyright 2023 The Dapr Authors
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

package reminders

import (
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/internal"
)

// Implements a reminders provider.
type reminders struct {
	clock             clock.WithTicker
	executeReminderFn internal.ExecuteReminderFn
	stateStore        internal.TransactionalStateStore
}

// NewRemindersProvider returns a TimerProvider.
func NewRemindersProvider(clock clock.WithTicker) internal.RemindersProvider {
	return &reminders{
		clock: clock,
	}
}

func (r *reminders) SetExecuteReminderFn(fn internal.ExecuteReminderFn) {
	r.executeReminderFn = fn
}

func (r *reminders) SetStateStore(store internal.TransactionalStateStore) {
	r.stateStore = store
}
