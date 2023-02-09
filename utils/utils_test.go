/*
Copyright 2021 The Dapr Authors
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

package utils

import (
	"net"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContains(t *testing.T) {
	type customType struct {
		v1 string
		v2 int
	}

	t.Run("find a item", func(t *testing.T) {
		assert.True(t, Contains([]string{"item-1", "item"}, "item"))
		assert.True(t, Contains([]int{1, 2, 3}, 1))
		assert.True(t, Contains([]customType{{v1: "first", v2: 1}, {v1: "second", v2: 2}}, customType{v1: "second", v2: 2}))
	})

	t.Run("didn't find a item", func(t *testing.T) {
		assert.False(t, Contains([]string{"item-1", "item"}, "not-in-item"))
		assert.False(t, Contains([]string{}, "not-in-item"))
		assert.False(t, Contains(nil, "not-in-item"))
		assert.False(t, Contains([]int{1, 2, 3}, 100))
		assert.False(t, Contains([]int{}, 100))
		assert.False(t, Contains(nil, 100))
		assert.False(t, Contains([]customType{{v1: "first", v2: 1}, {v1: "second", v2: 2}}, customType{v1: "foo", v2: 100}))
		assert.False(t, Contains([]customType{}, customType{v1: "foo", v2: 100}))
		assert.False(t, Contains(nil, customType{v1: "foo", v2: 100}))
	})
}

func TestSetEnvVariables(t *testing.T) {
	t.Run("set environment variables success", func(t *testing.T) {
		err := SetEnvVariables(map[string]string{
			"testKey": "testValue",
		})
		assert.Nil(t, err)
		assert.Equal(t, "testValue", os.Getenv("testKey"))
	})
	t.Run("set environment variables failed", func(t *testing.T) {
		err := SetEnvVariables(map[string]string{
			"": "testValue",
		})
		assert.NotNil(t, err)
		assert.NotEqual(t, "testValue", os.Getenv(""))
	})
}

func TestIsYaml(t *testing.T) {
	testCases := []struct {
		input    string
		expected bool
	}{
		{
			input:    "a.yaml",
			expected: true,
		}, {
			input:    "a.yml",
			expected: true,
		}, {
			input:    "a.txt",
			expected: false,
		},
	}
	for _, tc := range testCases {
		assert.Equal(t, IsYaml(tc.input), tc.expected)
	}
}

func TestGetIntValFromStringVal(t *testing.T) {
	tcs := []struct {
		name     string
		val      int
		def      int
		expected int
	}{
		{
			name:     "value is not provided by user, default value is used",
			val:      0,
			def:      5,
			expected: 5,
		},
		{
			name:     "val is provided by user",
			val:      91,
			def:      5,
			expected: 91,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetIntValOrDefault(tc.val, tc.def)
			if actual != tc.expected {
				t.Errorf("expected %d, actual %d", tc.expected, actual)
			}
		})
	}
}

func TestEnvOrElse(t *testing.T) {
	t.Run("envOrElse should return else value when env var is not present", func(t *testing.T) {
		const elseValue, fakeEnVar = "fakeValue", "envVarThatDoesntExists"
		require.NoError(t, os.Unsetenv(fakeEnVar))

		assert.Equal(t, GetEnvOrElse(fakeEnVar, elseValue), elseValue)
	})

	t.Run("envOrElse should return env var value when env var is present", func(t *testing.T) {
		const elseValue, fakeEnVar, fakeEnvVarValue = "fakeValue", "envVarThatExists", "envVarValue"
		defer os.Unsetenv(fakeEnVar)

		require.NoError(t, os.Setenv(fakeEnVar, fakeEnvVarValue))
		assert.Equal(t, GetEnvOrElse(fakeEnVar, elseValue), fakeEnvVarValue)
	})
}

func TestSocketExists(t *testing.T) {
	// Unix Domain Socket does not work on windows.
	if runtime.GOOS == "windows" {
		return
	}
	t.Run("socket exists should return false if file does not exists", func(t *testing.T) {
		assert.False(t, SocketExists("/fake/path"))
	})

	t.Run("socket exists should return false if file exists but it's not a socket", func(t *testing.T) {
		file, err := os.CreateTemp("/tmp", "prefix")
		require.NoError(t, err)
		defer os.Remove(file.Name())

		assert.False(t, SocketExists(file.Name()))
	})

	t.Run("socket exists should return true if file exists and its a socket", func(t *testing.T) {
		const fileName = "/tmp/socket1234.sock"
		defer os.Remove(fileName)
		listener, err := net.Listen("unix", fileName)
		require.NoError(t, err)
		defer listener.Close()

		assert.True(t, SocketExists(fileName))
	})
}

func TestPopulateMetadataForBulkPublishEntry(t *testing.T) {
	entryMeta := map[string]string{
		"key1": "val1",
		"ttl":  "22s",
	}

	t.Run("req Meta does not contain any key present in entryMeta", func(t *testing.T) {
		reqMeta := map[string]string{
			"rawPayload": "true",
			"key2":       "val2",
		}
		resMeta := PopulateMetadataForBulkPublishEntry(reqMeta, entryMeta)
		assert.Equal(t, 4, len(resMeta), "expected length to match")
		assert.Contains(t, resMeta, "key1", "expected key to be present")
		assert.Equal(t, "val1", resMeta["key1"], "expected val to be equal")
		assert.Contains(t, resMeta, "key2", "expected key to be present")
		assert.Equal(t, "val2", resMeta["key2"], "expected val to be equal")
		assert.Contains(t, resMeta, "ttl", "expected key to be present")
		assert.Equal(t, "22s", resMeta["ttl"], "expected val to be equal")
		assert.Contains(t, resMeta, "rawPayload", "expected key to be present")
		assert.Equal(t, "true", resMeta["rawPayload"], "expected val to be equal")
	})
	t.Run("req Meta contains key present in entryMeta", func(t *testing.T) {
		reqMeta := map[string]string{
			"ttl":  "1m",
			"key2": "val2",
		}
		resMeta := PopulateMetadataForBulkPublishEntry(reqMeta, entryMeta)
		assert.Equal(t, 3, len(resMeta), "expected length to match")
		assert.Contains(t, resMeta, "key1", "expected key to be present")
		assert.Equal(t, "val1", resMeta["key1"], "expected val to be equal")
		assert.Contains(t, resMeta, "key2", "expected key to be present")
		assert.Equal(t, "val2", resMeta["key2"], "expected val to be equal")
		assert.Contains(t, resMeta, "ttl", "expected key to be present")
		assert.Equal(t, "22s", resMeta["ttl"], "expected val to be equal")
	})
}

func TestFilter(t *testing.T) {
	t.Run("should filter out empty values", func(t *testing.T) {
		in := []string{"", "a", "", "b", "", "c"}
		out := Filter(in, func(s string) bool {
			return s != ""
		})
		assert.Equal(t, 6, cap(in))
		assert.Equal(t, 3, cap(out))
		assert.Equal(t, []string{"a", "b", "c"}, out)
	})
	t.Run("should filter out empty values and return empty collection if all values are filtered out", func(t *testing.T) {
		in := []string{"", "", ""}
		out := Filter(in, func(s string) bool {
			return s != ""
		})
		assert.Equal(t, 3, cap(in))
		assert.Equal(t, 0, cap(out))
		assert.Equal(t, []string{}, out)
	})
}

func TestContainsPrefixed(t *testing.T) {
	tcs := []struct {
		name     string
		prefixes []string
		v        string
		want     bool
	}{
		{
			name: "empty",
			v:    "some-service-account-name",
			want: false,
		},
		{
			name:     "notFound",
			v:        "some-service-account-name",
			prefixes: []string{"service-account-name", "other-service-account-name"},
			want:     false,
		},
		{
			name:     "one",
			v:        "some-service-account-name",
			prefixes: []string{"service-account-name", "some-service-account-name"},
			want:     true,
		},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equalf(t, tc.want, ContainsPrefixed(tc.prefixes, tc.v), "ContainsPrefixed(%v, %v)", tc.prefixes, tc.v)
		})
	}
}

func TestMapToSlice(t *testing.T) {
	t.Run("mapStringString", func(t *testing.T) {
		m := map[string]string{"a": "b", "c": "d", "e": "f"}
		got := MapToSlice(m)
		assert.ElementsMatch(t, got, []string{"a", "c", "e"})
	})
	t.Run("mapStringStruct", func(t *testing.T) {
		m := map[string]struct{}{"a": {}, "c": {}, "e": {}}
		got := MapToSlice(m)
		assert.ElementsMatch(t, got, []string{"a", "c", "e"})
	})
	t.Run("intStringStruct", func(t *testing.T) {
		m := map[int]struct{}{1: {}, 2: {}, 3: {}}
		got := MapToSlice(m)
		assert.ElementsMatch(t, got, []int{1, 2, 3})
	})
}

func TestSliceToMap(t *testing.T) {
	t.Run("sliceStringToMapStringString", func(t *testing.T) {
		m := []string{"a", "b", "c"}
		got := SliceToMap[string, string](m)
		assert.Equal(t, got, map[string]string{"a": "", "b": "", "c": ""})
	})
	t.Run("sliceStringToMapStringStruct", func(t *testing.T) {
		m := []string{"a", "b", "c"}
		got := SliceToMap[string, struct{}](m)
		assert.Equal(t, got, map[string]struct{}{"a": {}, "b": {}, "c": {}})
	})
	t.Run("sliceIntToMapIntBool", func(t *testing.T) {
		m := []int{1, 2, 3}
		got := SliceToMap[int, bool](m)
		assert.Equal(t, got, map[int]bool{1: false, 2: false, 3: false})
	})
}
