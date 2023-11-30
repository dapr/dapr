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
			name:          "TestRetryOnHttpCode",
			retryOn:       []string{"200,201,202,203,204", "300-309", "400", "500,505-509"},
			expectError:   false,
			testCodes:     []int32{200, 204, 300, 310, 400, 500, 509},
			expectedRetry: []bool{true, true, true, false, true, true, true},
		},
		{
			name:          "TestRetryOnGrpcCode",
			retryOn:       []string{"2", "8-10", "11,13-15"},
			expectError:   false,
			testCodes:     []int32{2, 3, 4, 5, 6, 10, 11, 12, 13},
			expectedRetry: []bool{true, false, false, false, false, true, true, false, true},
		},
		{
			name:          "TestIgnoreOnHttpCode",
			ignoreOn:      []string{"400-499", "500"},
			expectError:   false,
			testCodes:     []int32{200, 300, 400, 500},
			expectedRetry: []bool{true, true, false, false},
		},
		{
			name:          "TestIgnoreOnGrpcCode",
			ignoreOn:      []string{"4-10", "15"},
			expectError:   false,
			testCodes:     []int32{2, 4, 6, 12, 15},
			expectedRetry: []bool{true, false, false, true, false},
		},
		{
			name:        "TestRetryOnAndIgnoreOn",
			retryOn:     []string{"200-299"},
			ignoreOn:    []string{"200"},
			expectError: true,
		},
		{
			name:        "TestInvalidStartCodeBiggerThanEndCode",
			retryOn:     []string{"399-299"},
			expectError: true,
		},
		{
			name:        "TestInvalidCodeRange",
			retryOn:     []string{"199-299-300"},
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
