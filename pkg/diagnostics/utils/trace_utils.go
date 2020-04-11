// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	"strconv"
)

// IsTracingEnabled checks for tracing enabled or not based on given sampling rate
func IsTracingEnabled(rate string) bool {
	f, _ := strconv.ParseFloat(rate, 64)
	if f != 0 {
		return true
	}
	return false
}

// GetTraceSamplingRate parses the given rate and returns the parsed rate
func GetTraceSamplingRate(rate string) float64 {
	f, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		return 0
	}
	return f
}
