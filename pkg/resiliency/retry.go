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

	"github.com/dapr/kit/retry"
)

type CodeError struct {
	StatusCode int32
	err        error
}

func (ce CodeError) Error() string {
	return ce.err.Error()
}

func (ce CodeError) Unwrap() error {
	return ce.err
}

func NewCodeError(statusCode int32, err error) CodeError {
	return CodeError{
		StatusCode: statusCode,
		err:        err,
	}
}

type Retry struct {
	retry.Config
	StatusCodeFilter
}

func NewRetry(retryConfig retry.Config, statusCodeFilter StatusCodeFilter) *Retry {
	return &Retry{
		Config:           retryConfig,
		StatusCodeFilter: statusCodeFilter,
	}
}

type StatusCodeFilter struct {
	retryOnPatterns   []codeRange
	noRetryOnPatterns []codeRange
	defaultRetry      bool
}

// codeRange represents a range of status codes from start to end.
type codeRange struct {
	start int32
	end   int32
}

func (cr codeRange) MatchCode(c int32) bool {
	return cr.start <= c && c <= cr.end
}

func NewStatusCodeFilter() StatusCodeFilter {
	return StatusCodeFilter{defaultRetry: true}
}

func ParseStatusCodeFilter(retryOnPatterns string) (StatusCodeFilter, error) {
	filter := NewStatusCodeFilter()
	// if retryOn is set, parse retry on patterns and set default retry to false
	if retryOnPatterns != "" {
		parsedPatterns, err := filter.parsePatterns(retryOnPatterns)
		if err != nil {
			return filter, err
		}
		filter.retryOnPatterns = parsedPatterns
		filter.defaultRetry = false
	}
	return filter, nil
}

func (f StatusCodeFilter) parsePatterns(patterns string) ([]codeRange, error) {
	parsedPatterns := make([]codeRange, 0, len(patterns))
	splitted := strings.Split(patterns, ",")
	for _, p := range splitted {
		// make sure empty patterns are ignored
		if len(p) == 0 {
			continue
		}
		parsedPattern, err := compilePattern(p)
		if err != nil {
			return nil, err
		}
		parsedPatterns = append(parsedPatterns, parsedPattern)
	}
	return parsedPatterns, nil
}

func (f StatusCodeFilter) StatusCodeNeedRetry(statusCode int32) bool {
	// Check retriable codes (allowlist)
	for _, pattern := range f.retryOnPatterns {
		if pattern.MatchCode(statusCode) {
			return true
		}
	}

	// Check none retriable codes (denylist)
	for _, pattern := range f.noRetryOnPatterns {
		if pattern.MatchCode(statusCode) {
			return false
		}
	}

	return f.defaultRetry
}

func compilePattern(pattern string) (codeRange, error) {
	patterns := strings.Split(pattern, "-")
	if len(patterns) > 2 {
		return codeRange{}, fmt.Errorf("invalid pattern %s, more than one '-' exists", pattern)
	}
	// empty string is not allowed by strconv.Atoi, so we do not need to check empty string here
	start, err := strconv.Atoi(patterns[0])
	if err != nil {
		return codeRange{}, fmt.Errorf("failed to parse start code %s from pattern %s to int, %w", patterns[0], pattern, err)
	}
	if !validCode(start) {
		return codeRange{}, fmt.Errorf("invalid pattern %s, start code %d is not valid", pattern, start)
	}

	end := start
	if len(patterns) == 2 {
		end, err = strconv.Atoi(patterns[1])
		if err != nil {
			return codeRange{}, fmt.Errorf("failed to parse end code %s from pattern %s to int, %w", patterns[1], pattern, err)
		}
	}
	if !validCode(end) {
		return codeRange{}, fmt.Errorf("invalid pattern %s, end code %d is not valid", pattern, end)
	}

	if end < start {
		return codeRange{}, fmt.Errorf("invalid pattern %s, start value should not bigger than end value", pattern)
	}

	//nolint:gosec
	return codeRange{start: int32(start), end: int32(end)}, nil
}

// validCode checks if the code is valid.
// For grpc, the code should be less than 100.
// For http, the code should be 100 - 599.
func validCode(code int) bool {
	return code < 600
}
