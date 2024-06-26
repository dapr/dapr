package distributionbuckets

import (
	"fmt"
	"strconv"
	"strings"
)

func getBucketFromKey(t *testing.T, k string) float64 {
	// k = "a:b|le:5000"
	keyParts := strings.Split(k, "|")
	for _, k := range keyParts {
		if v, ok := strings.CutPrefix(k, "le:"); ok {
			d, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return 0, err
			}
			return float64(d), nil
		}
	}
	return 0, fmt.Errorf("did not find any bucket ('le') in key")
}
