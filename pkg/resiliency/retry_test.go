package resiliency

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	resiliencyv1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
)

func TestResiliencyFunctions(t *testing.T) {
	protocolHTTP := "HTTP"
	protocolGRPC := "gRPC"

	testCases := []struct {
		name          string
		conditions    *resiliencyv1alpha.RetryConditions
		expectError   bool
		protocol      string
		testCodes     []int32
		expectedRetry []bool
	}{
		{
			name:          "TestRetryHTTPOnNil",
			conditions:    nil,
			expectError:   false,
			protocol:      protocolHTTP,
			testCodes:     []int32{200, 204, 300, 310, 400, 500, 509},
			expectedRetry: []bool{true, true, true, true, true, true, true},
		},
		{
			name: "TestRetryOnHttpCode",
			conditions: &resiliencyv1alpha.RetryConditions{
				HTTPStatusCodes: "200,201,202,203,204,300-309,400,500,505-509",
			},
			expectError:   false,
			protocol:      protocolHTTP,
			testCodes:     []int32{200, 204, 300, 310, 400, 500, 509},
			expectedRetry: []bool{true, true, true, false, true, true, true},
		},
		{
			name: "TestRetryOnGrpcCode",
			conditions: &resiliencyv1alpha.RetryConditions{
				GRPCStatusCodes: "2,8-10,11,13-15",
			},
			expectError:   false,
			protocol:      protocolGRPC,
			testCodes:     []int32{2, 3, 4, 5, 6, 10, 11, 12, 13},
			expectedRetry: []bool{true, false, false, false, false, true, true, false, true},
		},
		{
			name: "TestInvalidStartCodeBiggerThanEndCode",
			conditions: &resiliencyv1alpha.RetryConditions{
				HTTPStatusCodes: "399-299",
			},
			expectError: true,
		},
		{
			name: "TestTryUsingGRPCCodesInHTTP",
			conditions: &resiliencyv1alpha.RetryConditions{
				HTTPStatusCodes: "1,2,3,4",
			},
			expectError: true,
		},
		{
			name: "TestInvalidCodeRangeFormat",
			conditions: &resiliencyv1alpha.RetryConditions{
				HTTPStatusCodes: "199-299-300",
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			filter, err := ParseRetryConditionFilter(tc.conditions)
			if tc.expectError {
				require.Error(t, err)
				return
			} else {
				require.NoError(t, err)
			}

			for i, code := range tc.testCodes {
				retry := filter.statusCodeNeedRetry(code)
				assert.Equal(t, tc.expectedRetry[i], retry)
			}
		})
	}
}
