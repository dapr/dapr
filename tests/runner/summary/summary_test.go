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

package summary

import (
	"encoding/json"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummary(t *testing.T) {
	t.Run("flush should save file", func(t *testing.T) {
		dir := t.TempDir()
		prefix := path.Join(dir, "test_report")
		t.Setenv("TEST_OUTPUT_FILE_PREFIX", prefix)
		summary := ForTest(t)
		summary.Output("test", "test").OutputInt("test", 2).Outputf("test", "%s", "1")
		require.NoError(t, summary.Flush())
		f, err := os.ReadFile(filePath(prefix, t.Name()))
		require.NoError(t, err)
		tab := &Table{}
		require.NoError(t, json.Unmarshal(f, &tab))
		assert.Len(t, tab.Data, 3)
		assert.Equal(t, tab.Test, t.Name())
	})
}
