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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
)

// handleReminder dispatches on name prefixes, so a held operation's name must
// not start with any name or prefix an earlier arm matches.
func TestHeldReminderNamesDoNotCollide(t *testing.T) {
	earlier := []string{
		janitorReminderName,
		reminderPrefixStart,
		reminderPrefixNewEvent,
		reminderPrefixTimer,
		reminderCascadeTerminate,
		reminderNameParentNotify,
		common.ReminderPrefixActivityResult,
	}

	for _, held := range []string{common.ReminderNameHeldStart, common.ReminderPrefixHeldEvent + "x"} {
		for _, prefix := range earlier {
			assert.Falsef(t, strings.HasPrefix(held, prefix), "held reminder name %q is caught by %q", held, prefix)
		}
	}
}
