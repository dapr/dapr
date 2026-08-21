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

package options

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateCARenewal(t *testing.T) {
	t.Run("defaults are valid", func(t *testing.T) {
		opts := New(nil)
		require.NoError(t, opts.Validate())
	})

	t.Run("threshold must be a fraction in (0, 1)", func(t *testing.T) {
		for _, threshold := range []string{"0", "1", "-0.5", "1.5"} {
			opts := New([]string{"--ca-renewal-threshold=" + threshold})
			require.ErrorContains(t, opts.Validate(), "ca-renewal-threshold must be in the range (0, 1)", "threshold %s", threshold)
		}

		opts := New([]string{"--ca-renewal-threshold=0.5"})
		require.NoError(t, opts.Validate())
	})

	t.Run("grace must fit in the issuer validity remaining when renewal fires", func(t *testing.T) {
		// 0.9 of 100h leaves 10h remaining; a 20h grace cannot fit.
		opts := New([]string{
			"--ca-ttl=100h",
			"--ca-renewal-threshold=0.9",
			"--trust-anchor-propagation-grace=20h",
		})
		require.ErrorContains(t, opts.Validate(), "trust-anchor-propagation-grace")

		opts = New([]string{
			"--ca-ttl=100h",
			"--ca-renewal-threshold=0.9",
			"--trust-anchor-propagation-grace=5h",
		})
		require.NoError(t, opts.Validate())
	})

	t.Run("grace must be positive", func(t *testing.T) {
		opts := New([]string{"--trust-anchor-propagation-grace=0s"})
		require.ErrorContains(t, opts.Validate(), "trust-anchor-propagation-grace must be greater than zero")
	})

	t.Run("no renewal validation when disabled", func(t *testing.T) {
		opts := New([]string{
			"--ca-renewal-enabled=false",
			"--ca-renewal-threshold=42",
			"--trust-anchor-propagation-grace=0s",
		})
		assert.NoError(t, opts.Validate())
	})
}
