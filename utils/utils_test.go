// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

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
		assert.Equal(t, "2020-01-02T15:04:05.123000Z", isoString)
	})

	t.Run("succeed to parse generated iso8601 string to time.Time using RFC3339 Parser", func(t *testing.T) {
		currentTime := time.Now()
		isoString := ToISO8601DateTimeString(currentTime)
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
			testName:  "valid environment string.",
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
