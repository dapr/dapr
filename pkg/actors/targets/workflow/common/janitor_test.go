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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvInt64Or(t *testing.T) {
	const name = "DAPR_TEST_ENV_INT64_OR"

	tests := map[string]struct {
		value string
		set   bool
		want  int64
	}{
		"unset":    {want: 7},
		"empty":    {set: true, value: "", want: 7},
		"zero":     {set: true, value: "0", want: 0},
		"positive": {set: true, value: "42", want: 42},
		"negative": {set: true, value: "-1", want: 7},
		"invalid":  {set: true, value: "abc", want: 7},
	}

	for desc, tc := range tests {
		t.Run(desc, func(t *testing.T) {
			if tc.set {
				t.Setenv(name, tc.value)
			}
			assert.Equal(t, tc.want, EnvInt64Or(name, 7))
		})
	}
}
