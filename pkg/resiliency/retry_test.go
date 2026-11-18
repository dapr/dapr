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

package resiliency

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	resiliencyv1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
)

func TestResiliencyFunctions(t *testing.T) {
	protocolHTTP := "HTTP"
	protocolGRPC := "gRPC"

	testCases := []struct {
		name          string
		matching      *resiliencyv1alpha.RetryMatching
		expectError   bool
		protocol      string
		testCodes     []int32
		expectedRetry []bool
	}{
		{
			name:          "TestRetryHTTPOnNil",
			matching:      nil,
			expectError:   false,
			protocol:      protocolHTTP,
			testCodes:     []int32{200, 204, 300, 310, 400, 500, 509},
			expectedRetry: []bool{true, true, true, true, true, true, true},
		},
		{
			name: "TestRetryOnHttpCode",
			matching: &resiliencyv1alpha.RetryMatching{
				HTTPStatusCodes: "200,201,202,203,204,300-309,400,500,505-509",
			},
			expectError:   false,
			protocol:      protocolHTTP,
			testCodes:     []int32{200, 204, 300, 310, 400, 500, 509},
			expectedRetry: []bool{true, true, true, false, true, true, true},
		},
		{
			name: "TestRetryOnGrpcCode",
			matching: &resiliencyv1alpha.RetryMatching{
				GRPCStatusCodes: "2,8-10,11,13-15",
			},
			expectError:   false,
			protocol:      protocolGRPC,
			testCodes:     []int32{2, 3, 4, 5, 6, 10, 11, 12, 13},
			expectedRetry: []bool{true, false, false, false, false, true, true, false, true},
		},
		{
			name: "TestInvalidStartCodeBiggerThanEndCode",
			matching: &resiliencyv1alpha.RetryMatching{
				HTTPStatusCodes: "399-299",
			},
			expectError: true,
		},
		{
			name: "TestTryUsingGRPCCodesInHTTP",
			matching: &resiliencyv1alpha.RetryMatching{
				HTTPStatusCodes: "1,2,3,4",
			},
			expectError: true,
		},
		{
			name: "TestInvalidCodeRangeFormat",
			matching: &resiliencyv1alpha.RetryMatching{
				HTTPStatusCodes: "199-299-300",
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			match, err := ParseRetryConditionMatch(tc.matching)
			if tc.expectError {
				require.Error(t, err)
				return
			} else {
				require.NoError(t, err)
			}

			for i, code := range tc.testCodes {
				retry := match.statusCodeNeedRetry(code)
				assert.Equal(t, tc.expectedRetry[i], retry)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		value  string
		want   time.Duration
		wantOK bool
	}{
		{name: "seconds", value: "5", want: 5 * time.Second, wantOK: true},
		{name: "seconds with spaces", value: " 2 ", want: 2 * time.Second, wantOK: true},
		{name: "zero", value: "0", want: 0, wantOK: true},
		{name: "http date in the future", value: now.Add(30 * time.Second).Format(http.TimeFormat), want: 30 * time.Second, wantOK: true},
		{name: "http date in the past", value: now.Add(-time.Minute).Format(http.TimeFormat), want: 0, wantOK: true},
		{name: "huge value is clamped", value: "99999999999999999", want: time.Duration(maxRetryAfterSeconds) * time.Second, wantOK: true},
		{name: "empty", value: "", wantOK: false},
		{name: "negative", value: "-1", wantOK: false},
		{name: "fraction", value: "1.5", wantOK: false},
		{name: "garbage", value: "soon", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseRetryAfter(tt.value, now)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCodeErrorWithRetryAfterHeader(t *testing.T) {
	err := errors.New("boom")

	t.Run("429 keeps the delay", func(t *testing.T) {
		ce := NewCodeError(http.StatusTooManyRequests, err).WithRetryAfterHeader("3")
		assert.Equal(t, 3*time.Second, ce.RetryAfter())
		assert.Equal(t, err, ce.Unwrap())
	})

	t.Run("503 keeps the delay", func(t *testing.T) {
		ce := NewCodeError(http.StatusServiceUnavailable, err).WithRetryAfterHeader("1")
		assert.Equal(t, time.Second, ce.RetryAfter())
	})

	t.Run("other status codes ignore the header", func(t *testing.T) {
		ce := NewCodeError(http.StatusInternalServerError, err).WithRetryAfterHeader("3")
		assert.Zero(t, ce.RetryAfter())
	})

	t.Run("gRPC codes ignore the header", func(t *testing.T) {
		ce := NewCodeError(8, err).WithRetryAfterHeader("3")
		assert.Zero(t, ce.RetryAfter())
	})

	t.Run("missing or invalid header", func(t *testing.T) {
		assert.Zero(t, NewCodeError(http.StatusTooManyRequests, err).WithRetryAfterHeader("").RetryAfter())
		assert.Zero(t, NewCodeError(http.StatusTooManyRequests, err).WithRetryAfterHeader("later").RetryAfter())
	})
}
