// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	"strconv"
)

const (
	defaultSamplingRate = 1e-4
)

// GetTraceSamplingRate parses the given rate and returns the parsed rate
func GetTraceSamplingRate(rate string) float64 {
	f, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		return defaultSamplingRate
	}
	return f
}
