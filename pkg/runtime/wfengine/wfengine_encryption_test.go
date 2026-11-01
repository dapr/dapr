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

package wfengine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/config"
)

func TestNewEncryptor(t *testing.T) {
	t.Parallel()

	spec := func(enc *config.WorkflowEncryptionSpec) *config.WorkflowSpec {
		return &config.WorkflowSpec{Encryption: enc}
	}
	valid := &config.WorkflowEncryptionSpec{CryptoComponent: "kv", KeyName: "kek"}

	tests := map[string]struct {
		opts    Options
		enabled bool
		err     string
	}{
		"no spec": {
			opts: Options{},
		},
		"no encryption section": {
			opts: Options{Spec: &config.WorkflowSpec{}},
		},
		"encryption section": {
			opts:    Options{Spec: spec(valid)},
			enabled: true,
		},
		"no key name": {
			opts: Options{Spec: spec(&config.WorkflowEncryptionSpec{CryptoComponent: "kv"})},
			err:  "crypto component and key name are required",
		},
		"unsupported algorithm": {
			opts: Options{Spec: spec(&config.WorkflowEncryptionSpec{CryptoComponent: "kv", KeyName: "kek", KeyWrapAlgorithm: "A128CBC"})},
			err:  "unsupported key wrap algorithm",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			enc, err := newEncryptor(tc.opts)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.enabled, enc != nil)
		})
	}
}
