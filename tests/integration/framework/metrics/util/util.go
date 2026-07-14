/*
Copyright 2024 The Dapr Authors
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

package util

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// GetBucketFromKey returns a bucket given a key
// k = "a:b|le:5000"
func GetBucketFromKey(t *testing.T, k string) float64 {
	t.Helper()
	keyParts := strings.SplitSeq(k, "|")
	for k := range keyParts {
		if v, ok := strings.CutPrefix(k, "le:"); ok {
			d, err := strconv.ParseUint(v, 10, 64)
			require.NoError(t, err)
			return float64(d)
		}
	}
	t.Error("did not find any bucket ('le') in key")
	return 0
}

func CollectBuckets(t *testing.T, metrics map[string]float64, metric, name, status string) []float64 {
	t.Helper()

	var buckets []float64
	for m := range metrics {
		if strings.HasPrefix(m, metric) && strings.Contains(m, name) && strings.Contains(m, status) {
			bucket := GetBucketFromKey(t, m)
			buckets = append(buckets, bucket)
		}
	}

	slices.Sort(buckets)

	return buckets
}
