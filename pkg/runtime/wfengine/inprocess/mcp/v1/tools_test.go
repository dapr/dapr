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

package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateToolArguments(t *testing.T) {
	schemas := &toolSchemaCache{}

	t.Run("no cached schema passes validation", func(t *testing.T) {
		msg := validateToolArguments(schemas, "greet", map[string]any{"name": "dapr"})
		assert.Empty(t, msg)
	})

	t.Run("schema with no required field passes", func(t *testing.T) {
		setTestSchema(t, schemas, "greet", map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
		})
		msg := validateToolArguments(schemas, "greet", map[string]any{})
		assert.Empty(t, msg)
	})

	t.Run("missing required argument fails", func(t *testing.T) {
		setTestSchema(t, schemas, "weather", map[string]any{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []any{"city"},
		})
		msg := validateToolArguments(schemas, "weather", map[string]any{})
		assert.Contains(t, msg, "city")
		assert.Contains(t, msg, "missing required")
	})

	t.Run("all required arguments present passes", func(t *testing.T) {
		msg := validateToolArguments(schemas, "weather", map[string]any{"city": "Portland"})
		assert.Empty(t, msg)
	})

	t.Run("multiple missing required arguments listed", func(t *testing.T) {
		setTestSchema(t, schemas, "multi", map[string]any{
			"type":     "object",
			"required": []any{"a", "b", "c"},
		})
		msg := validateToolArguments(schemas, "multi", map[string]any{"b": 1})
		assert.Contains(t, msg, "a")
		assert.Contains(t, msg, "c")
		assert.NotContains(t, msg, "\"b\"")
	})
}
