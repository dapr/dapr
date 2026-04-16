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

package method

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeMethod(t *testing.T) {
	t.Run("rejects hash", func(t *testing.T) {
		_, err := NormalizeMethod("test#stream")
		require.Error(t, err)
	})

	t.Run("rejects question mark", func(t *testing.T) {
		_, err := NormalizeMethod("test?stream")
		require.Error(t, err)
	})

	t.Run("rejects null byte", func(t *testing.T) {
		_, err := NormalizeMethod("test\x00stream")
		require.Error(t, err)
	})

	t.Run("rejects control characters", func(t *testing.T) {
		_, err := NormalizeMethod("test\x01stream")
		require.Error(t, err)
		_, err = NormalizeMethod("test\tstream")
		require.Error(t, err)
		_, err = NormalizeMethod("test\nstream")
		require.Error(t, err)
		_, err = NormalizeMethod("test\rstream")
		require.Error(t, err)
	})

	t.Run("allows percent", func(t *testing.T) {
		got, err := NormalizeMethod("test%stream")
		require.NoError(t, err)
		assert.Equal(t, "test%stream", got)
	})

	t.Run("allows percent-encoded sequences as raw", func(t *testing.T) {
		got, err := NormalizeMethod("test%25stream")
		require.NoError(t, err)
		assert.Equal(t, "test%25stream", got)
	})

	t.Run("allows double quote", func(t *testing.T) {
		got, err := NormalizeMethod(`test"stream`)
		require.NoError(t, err)
		assert.Equal(t, `test"stream`, got)
	})

	t.Run("allows asterisk", func(t *testing.T) {
		got, err := NormalizeMethod("test*stream")
		require.NoError(t, err)
		assert.Equal(t, "test*stream", got)
	})

	t.Run("allows backslash", func(t *testing.T) {
		got, err := NormalizeMethod(`test\stream`)
		require.NoError(t, err)
		assert.Equal(t, `test\stream`, got)
	})

	t.Run("resolves traversal", func(t *testing.T) {
		got, err := NormalizeMethod("admin/../public")
		require.NoError(t, err)
		assert.Equal(t, "public", got)
	})

	t.Run("resolves leading traversal", func(t *testing.T) {
		got, err := NormalizeMethod("../../etc/passwd")
		require.NoError(t, err)
		assert.Equal(t, "etc/passwd", got)
	})

	t.Run("simple method unchanged", func(t *testing.T) {
		got, err := NormalizeMethod("mymethod")
		require.NoError(t, err)
		assert.Equal(t, "mymethod", got)
	})

	t.Run("method with slashes", func(t *testing.T) {
		got, err := NormalizeMethod("api/v1/users")
		require.NoError(t, err)
		assert.Equal(t, "api/v1/users", got)
	})
}

func TestValidateName(t *testing.T) {
	t.Run("rejects forward slash", func(t *testing.T) {
		require.Error(t, ValidateName("../../method/evil"))
	})

	t.Run("rejects backslash", func(t *testing.T) {
		require.Error(t, ValidateName(`test\evil`))
	})

	t.Run("rejects hash", func(t *testing.T) {
		require.Error(t, ValidateName("test#stream"))
	})

	t.Run("rejects question mark", func(t *testing.T) {
		require.Error(t, ValidateName("test?redirect=evil"))
	})

	t.Run("rejects null byte", func(t *testing.T) {
		require.Error(t, ValidateName("test\x00evil"))
	})

	t.Run("rejects carriage return", func(t *testing.T) {
		require.Error(t, ValidateName("test\revil"))
	})

	t.Run("rejects double-dot", func(t *testing.T) {
		require.Error(t, ValidateName(".."))
	})

	t.Run("rejects single dot", func(t *testing.T) {
		require.Error(t, ValidateName("."))
	})

	t.Run("allows simple name", func(t *testing.T) {
		require.NoError(t, ValidateName("myreminder"))
	})

	t.Run("allows name with hyphens", func(t *testing.T) {
		require.NoError(t, ValidateName("my-reminder-1"))
	})

	t.Run("allows name with dots", func(t *testing.T) {
		require.NoError(t, ValidateName("my.reminder"))
	})

	t.Run("allows percent", func(t *testing.T) {
		require.NoError(t, ValidateName("test%25stream"))
	})
}
