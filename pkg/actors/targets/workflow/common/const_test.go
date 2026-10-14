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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActivityResultReminderName(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		reminder string
		execID   string
	}{
		"carries the execution ID":        {ActivityResultReminderName("aBc-_1", "4d3c2b1a-0000-4000-8000-000000000000"), "4d3c2b1a-0000-4000-8000-000000000000"},
		"no execution ID":                 {ActivityResultReminderName("aBc-_1", ""), ""},
		"written by a daprd predating it": {ReminderPrefixActivityResult + "aBc-_1", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, strings.HasPrefix(tc.reminder, ReminderPrefixActivityResult), "every name keeps the prefix older daprds match on")
			assert.Equal(t, tc.execID, ActivityResultParentExecutionID(tc.reminder))
		})
	}
}
