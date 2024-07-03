/*
Copyright 2023 The Dapr Authors
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

func NewHTTPCodeError(statusCode int32, err error) CodeError {
	return CodeError{
		StatusCode:    statusCode,
		retryScenario: retryScenarioHTTPInvoke,
		err:           err,
	}
}

func NewGRPCCodeError(statusCode int32, err error) CodeError {
	return CodeError{
		StatusCode:    statusCode,
		retryScenario: retryScenarioGRPCInvoke,
		err:           err,
	}
}

type Retry struct {
	retry.Config
	RetryConditionFilter
}

func NewRetry(retryConfig retry.Config, statusCodeFilter RetryConditionFilter) *Retry {
	return &Retry{
		Config:               retryConfig,
		RetryConditionFilter: statusCodeFilter,
	}
}

type RetryConditionFilter struct {
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

func NewRetryConditionFilter() RetryConditionFilter {
	return RetryConditionFilter{}
}

func ParseRetryConditionFilter(conditions *resiliencyV1alpha.RetryConditions) (RetryConditionFilter, error) {
	filter := NewRetryConditionFilter()
	if conditions != nil {
		httpStatusCodes, err := parsePatterns(conditions.HTTPStatusCodes, validHTTPStatusCodeRange)
		if err != nil {
			return filter, err
		}
		gRPCStatusCodes, err := parsePatterns(conditions.GRPCStatusCodes, validGRPCStatusCodeRange)
		if err != nil {
			return filter, err
		}
		// Only set in the end, to make sure all attributes are correct.
		filter.httpStatusCodes = httpStatusCodes
		filter.gRPCStatusCodes = gRPCStatusCodes
	}
	return filter, nil
}

func (f RetryConditionFilter) statusCodeNeedRetry(statusCode int32) bool {
	if validGRPCStatusCodeRange.matchCode(statusCode) {
		return mustRetryStatusCode(statusCode, f.gRPCStatusCodes)
	} else if validHTTPStatusCodeRange.matchCode(statusCode) {
		return mustRetryStatusCode(statusCode, f.httpStatusCodes)
	}
	return false
}

func (f RetryConditionFilter) ErrorNeedRetry(codeError CodeError) bool {
	switch codeError.retryScenario {
	case retryScenarioGRPCInvoke:
		return mustRetryStatusCode(codeError.StatusCode, f.gRPCStatusCodes)
	case retryScenarioHTTPInvoke:
		return mustRetryStatusCode(codeError.StatusCode, f.httpStatusCodes)
	}
	return false
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
