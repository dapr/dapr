package resiliency

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResiliencyFunctions(t *testing.T) {
	testCases := []struct {
		name          string
		retryOn       []string
		ignoreOn      []string
		expectError   bool
		testCodes     []int32
		expectedRetry []bool
	}{
		{
			name:          "TestRetryOn",
			retryOn:       []string{"2**", "30[0-9]", "400", "50[189]"},
			expectError:   false,
			testCodes:     []int32{200, 204, 300, 310, 400, 500, 509},
			expectedRetry: []bool{true, true, true, false, true, false, true},
		},
		{
			name:          "TestIgnoreOn",
			ignoreOn:      []string{"4**", "500"},
			expectError:   false,
			testCodes:     []int32{200, 300, 400, 500},
			expectedRetry: []bool{true, true, false, false},
		},
		{
			name:        "TestRetryOnAndIgnoreOn",
			retryOn:     []string{"2**"},
			ignoreOn:    []string{"200"},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			filter, err := ParseStatusCodeFilter(tc.retryOn, tc.ignoreOn)
			if tc.expectError {
				assert.Error(t, err)
				return
			} else {
				assert.NoError(t, err)
			}

			for i, code := range tc.testCodes {
				retry := filter.StatusCodeNeedRetry(code)
				assert.Equal(t, tc.expectedRetry[i], retry)
			}
		})
	}
}
