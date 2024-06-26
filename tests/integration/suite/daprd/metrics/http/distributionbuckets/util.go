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

package distributionbuckets

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func getBucketFromKey(t *testing.T, k string) float64 {
	t.Helper()
	// k = "a:b|le:5000"
	keyParts := strings.Split(k, "|")
	for _, k := range keyParts {
		if v, ok := strings.CutPrefix(k, "le:"); ok {
			d, err := strconv.ParseUint(v, 10, 64)
			require.NoError(t, err)
			return float64(d)
		}
	}
	t.Error("did not find any bucket ('le') in key")
	return 0
}
