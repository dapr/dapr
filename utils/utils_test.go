// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestToISO8601DateTimeString(t *testing.T) {
	t.Run("succeed to convert time.Time to ISO8601 datetime string", func(t *testing.T) {
		testDateTime, err := time.Parse(time.RFC3339, "2020-01-02T15:04:05.123Z")
		assert.NoError(t, err)
		isoString := ToISO8601DateTimeString(testDateTime)
		assert.Equal(t, "2020-01-02T15:04:05.123000Z", isoString)
	})

	t.Run("succeed to parse generated iso8601 string to time.Time using RFC3339 Parser", func(t *testing.T) {
		currentTime := time.Now().UTC()
		isoString := ToISO8601DateTimeString(currentTime)
		parsed, err := time.Parse(time.RFC3339, isoString)

		// assert
		assert.NoError(t, err)
		assert.Equal(t, currentTime.Year(), parsed.Year())
		assert.Equal(t, currentTime.Month(), parsed.Month())
		assert.Equal(t, currentTime.Day(), parsed.Day())
		assert.Equal(t, currentTime.Hour(), parsed.Hour())
		assert.Equal(t, currentTime.Minute(), parsed.Minute())
		assert.Equal(t, currentTime.Second(), parsed.Second())
		assert.Equal(t, currentTime.Nanosecond()/1000, parsed.Nanosecond()/1000)
	})
}
