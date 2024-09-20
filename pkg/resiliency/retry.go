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
	"fmt"
	"strconv"
	"strings"

	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/kit/retry"
)

var (
	// Reference: https://developer.mozilla.org/en-US/docs/Web/HTTP/Status
	validHTTPStatusCodeRange = codeRange{100, 599}
	// Reference: https://grpc.io/docs/guides/status-codes/
	validGRPCStatusCodeRange = codeRange{0, 16}
)

type retryScenario int

const (
	retryScenarioHTTPInvoke retryScenario = iota
	retryScenarioGRPCInvoke
)

type CodeError struct {
	StatusCode    int32
	retryScenario retryScenario
	err           error
}

func (ce CodeError) Error() string {
	return ce.err.Error()
}

func (ce CodeError) Unwrap() error {
	return ce.err
}

// NewCodeError is used when the statusCode can be either HTTP or gRPC
func NewCodeError(statusCode int32, err error) CodeError {
	return CodeError{
		StatusCode:    statusCode,
		retryScenario: detectErrorProto(statusCode),
		err:           err,
	}
}

type Retry struct {
	retry.Config
	RetryConditionMatch
}

func NewRetry(retryConfig retry.Config, statusCodeMatch RetryConditionMatch) *Retry {
	return &Retry{
		Config:              retryConfig,
		RetryConditionMatch: statusCodeMatch,
	}
}

type RetryConditionMatch struct {
	httpStatusCodes []codeRange
	gRPCStatusCodes []codeRange
}

// codeRange represents a range of status codes from start to end.
type codeRange struct {
	start int32
	end   int32
}

func (cr codeRange) matchCode(c int32) bool {
	return cr.start <= c && c <= cr.end
}

func NewRetryConditionMatch() RetryConditionMatch {
	return RetryConditionMatch{}
}

func ParseRetryConditionMatch(conditions *resiliencyV1alpha.RetryMatching) (RetryConditionMatch, error) {
	match := NewRetryConditionMatch()
	if conditions != nil {
		httpStatusCodes, err := parsePatterns(conditions.HTTPStatusCodes, validHTTPStatusCodeRange)
		if err != nil {
			return match, err
		}
		gRPCStatusCodes, err := parsePatterns(conditions.GRPCStatusCodes, validGRPCStatusCodeRange)
		if err != nil {
			return match, err
		}
		// Only set in the end, to make sure all attributes are correct.
		match.httpStatusCodes = httpStatusCodes
		match.gRPCStatusCodes = gRPCStatusCodes
	}
	return match, nil
}

func (f RetryConditionMatch) statusCodeNeedRetry(statusCode int32) bool {
	if validGRPCStatusCodeRange.matchCode(statusCode) {
		return mustRetryStatusCode(statusCode, f.gRPCStatusCodes)
	} else if validHTTPStatusCodeRange.matchCode(statusCode) {
		return mustRetryStatusCode(statusCode, f.httpStatusCodes)
	}
	return false
}

func (f RetryConditionMatch) ErrorNeedRetry(codeError CodeError) bool {
	switch codeError.retryScenario {
	case retryScenarioGRPCInvoke:
		return mustRetryStatusCode(codeError.StatusCode, f.gRPCStatusCodes)
	case retryScenarioHTTPInvoke:
		return mustRetryStatusCode(codeError.StatusCode, f.httpStatusCodes)
	}
	return false
}

func detectErrorProto(statusCode int32) retryScenario {
	if validHTTPStatusCodeRange.matchCode(statusCode) {
		return retryScenarioHTTPInvoke
	}

	return retryScenarioGRPCInvoke
}

func mustRetryStatusCode(statusCode int32, ranges []codeRange) bool {
	if ranges == nil {
		return true
	}

	// Check retriable codes (allowlist)
	for _, r := range ranges {
		if r.matchCode(statusCode) {
			return true
		}
	}

	return false
}

func parsePatterns(patterns string, validRange codeRange) ([]codeRange, error) {
	if patterns == "" {
		return nil, nil
	}

	parsedPatterns := make([]codeRange, 0, len(patterns))
	splitted := strings.Split(patterns, ",")
	for _, p := range splitted {
		// make sure empty patterns are ignored
		if len(p) == 0 {
			continue
		}
		parsedPattern, err := compilePattern(p, validRange)
		if err != nil {
			return nil, err
		}
		parsedPatterns = append(parsedPatterns, parsedPattern)
	}
	return parsedPatterns, nil
}

func compilePattern(pattern string, validRange codeRange) (codeRange, error) {
	patterns := strings.Split(pattern, "-")
	if len(patterns) > 2 {
		return codeRange{}, fmt.Errorf("invalid pattern %s, more than one '-' exists", pattern)
	}
	// empty string is not allowed by strconv.Atoi, so we do not need to check empty string here
	startint, err := strconv.Atoi(patterns[0])
	if err != nil {
		return codeRange{}, fmt.Errorf("failed to parse start code %s from pattern %s to int, %w", patterns[0], pattern, err)
	}
	//nolint:gosec
	start := int32(startint)
	if !validRange.matchCode(start) {
		return codeRange{}, fmt.Errorf("invalid pattern %s, start code %d is not valid", pattern, start)
	}

	endint := startint
	if len(patterns) == 2 {
		endint, err = strconv.Atoi(patterns[1])
		if err != nil {
			return codeRange{}, fmt.Errorf("failed to parse end code %s from pattern %s to int, %w", patterns[1], pattern, err)
		}
	}
	//nolint:gosec
	end := int32(endint)
	if !validRange.matchCode(end) {
		return codeRange{}, fmt.Errorf("invalid pattern %s, end code %d is not valid", pattern, end)
	}

	if end < start {
		return codeRange{}, fmt.Errorf("invalid pattern %s, start value should not bigger than end value", pattern)
	}

	return codeRange{start: start, end: end}, nil
}
