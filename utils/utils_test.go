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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestToISO8601DateTimeString(t *testing.T) {
	t.Run("succeed to convert time.Time to ISO8601 datetime string", func(t *testing.T) {
		testDateTime, err := time.Parse(time.RFC3339, "2020-01-02T15:04:05.123Z")
		assert.NoError(t, err)
		isoString := ToISO8601DateTimeString(testDateTime)
		assert.Equal(t, "2020-01-02T15:04:05.123Z", isoString)
	})

	t.Run("succeed to parse generated iso8601 string to time.Time using RFC3339 Parser", func(t *testing.T) {
		currentTime := time.Unix(1623306411, 123000)
		assert.Equal(t, 123000, currentTime.UTC().Nanosecond())
		isoString := ToISO8601DateTimeString(currentTime)
		assert.Equal(t, "2021-06-10T06:26:51.000123Z", isoString)
		parsed, err := time.Parse(time.RFC3339, isoString)

		// assert
		assert.NoError(t, err)
		assert.Equal(t, currentTime.UTC().Year(), parsed.Year())
		assert.Equal(t, currentTime.UTC().Month(), parsed.Month())
		assert.Equal(t, currentTime.UTC().Day(), parsed.Day())
		assert.Equal(t, currentTime.UTC().Hour(), parsed.Hour())
		assert.Equal(t, currentTime.UTC().Minute(), parsed.Minute())
		assert.Equal(t, currentTime.UTC().Second(), parsed.Second())
		assert.Equal(t, currentTime.UTC().Nanosecond()/1000, parsed.Nanosecond()/1000)
	})
}

func TestParseEnvString(t *testing.T) {
	testCases := []struct {
		testName  string
		envStr    string
		expEnvLen int
		expEnv    []corev1.EnvVar
	}{
		{
			testName:  "empty environment string.",
			envStr:    "",
			expEnvLen: 0,
			expEnv:    []corev1.EnvVar{},
		},
		{
			testName:  "common valid environment string.",
			envStr:    "ENV1=value1,ENV2=value2, ENV3=value3",
			expEnvLen: 3,
			expEnv: []corev1.EnvVar{
				{
					Name:  "ENV1",
					Value: "value1",
				},
				{
					Name:  "ENV2",
					Value: "value2",
				},
				{
					Name:  "ENV3",
					Value: "value3",
				},
			},
		},
		{
			testName:  "special valid environment string.",
			envStr:    `HTTP_PROXY=http://myproxy.com, NO_PROXY="localhost,127.0.0.1,.amazonaws.com"`,
			expEnvLen: 2,
			expEnv: []corev1.EnvVar{
				{
					Name:  "HTTP_PROXY",
					Value: "http://myproxy.com",
				},
				{
					Name:  "NO_PROXY",
					Value: `"localhost,127.0.0.1,.amazonaws.com"`,
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			envVars := ParseEnvString(tc.envStr)
			fmt.Println(tc.testName)
			assert.Equal(t, tc.expEnvLen, len(envVars))
			assert.Equal(t, tc.expEnv, envVars)
		})
	}
}

func TestStringSliceContains(t *testing.T) {
	t.Run("find a item", func(t *testing.T) {
		assert.True(t, StringSliceContains("item", []string{"item-1", "item"}))
	})

	t.Run("didn't find a item", func(t *testing.T) {
		assert.False(t, StringSliceContains("not-in-item", []string{}))
		assert.False(t, StringSliceContains("not-in-item", nil))
	})
}
