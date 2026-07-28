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

	"github.com/dapr/dapr/tests/integration/framework/metrics"

	"github.com/stretchr/testify/require"
)

// GetBucketFromKey returns a bucket given a key
// k = "a:b|le:5000"
//
// t is a require.TestingT so callers can pass either a *testing.T or, when
// invoked inside an EventuallyWithT retry loop, the *assert.CollectT. Passing
// the CollectT scopes scrape/parse failures to the retry loop instead of
// failing the outer test on a transient miss.
func GetBucketFromKey(t require.TestingT, k string) float64 {
	keyParts := strings.SplitSeq(k, "|")
	for part := range keyParts {
		if v, ok := strings.CutPrefix(part, "le:"); ok {
			d, err := strconv.ParseUint(v, 10, 64)
			require.NoError(t, err)
			return float64(d)
		}
	}
	require.Fail(t, "did not find any bucket ('le') in key")
	return 0
}

// CollectBuckets returns the sorted bucket boundaries for the histogram whose
// key matches metric/name/status. See GetBucketFromKey for why t is a
// require.TestingT.
func CollectBuckets(t require.TestingT, metrics *metrics.Metrics, metric, name, status string) []float64 {
	if metrics == nil {
		return nil
	}

	var buckets []float64
	for m := range metrics.All() {
		if strings.HasPrefix(m, metric) && strings.Contains(m, name) && strings.Contains(m, status) {
			bucket := GetBucketFromKey(t, m)
			buckets = append(buckets, bucket)
		}
	}

	slices.Sort(buckets)

	return buckets
}
