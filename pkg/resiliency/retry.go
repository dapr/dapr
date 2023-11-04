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
	"errors"
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
	retryOnPatterns  []codeRange
	ignoreOnPatterns []codeRange
	defaultRetry     bool
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

func ParseStatusCodeFilter(retryOnPatterns []string, ignoreOnPatterns []string) (StatusCodeFilter, error) {
	filter := NewStatusCodeFilter()
	if len(retryOnPatterns) != 0 && len(ignoreOnPatterns) != 0 {
		return filter, errors.New("retryOnCodes and ignoreOnCodes cannot be used together")
	}
	// if retryOn is set, parse retry on patterns and set default retry to false
	if len(retryOnPatterns) != 0 {
		parsedPatterns, err := filter.parsePatterns(retryOnPatterns)
		if err != nil {
			return filter, err
		}
		filter.retryOnPatterns = parsedPatterns
		filter.defaultRetry = false
	}
	// if ignoreOn is set, parse ignore on patterns and set default retry to true
	if len(ignoreOnPatterns) != 0 {
		parsedPatterns, err := filter.parsePatterns(ignoreOnPatterns)
		if err != nil {
			return filter, err
		}
		filter.ignoreOnPatterns = parsedPatterns
		filter.defaultRetry = true
	}
	return filter, nil
}

func (f StatusCodeFilter) parsePatterns(patterns []string) ([]codeRange, error) {
	parsedPatterns := make([]codeRange, 0, len(patterns))
	for _, item := range patterns {
		// one item can contain multiple patterns separated by comma
		splited := strings.Split(item, ",")
		for _, p := range splited {
			parsedPattern, err := compilePattern(p)
			if err != nil {
				return nil, fmt.Errorf("failed to compile item %s, %w", item, err)
			}
			parsedPatterns = append(parsedPatterns, parsedPattern)
		}
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

	// Check ignored codes (denylist)
	for _, pattern := range f.ignoreOnPatterns {
		if pattern.MatchCode(statusCode) {
			return false
		}
	}

	return f.defaultRetry
}

func compilePattern(pattern string) (codeRange, error) {
	// parse code range pattern
	if strings.Contains(pattern, "-") {
		patterns := strings.Split(pattern, "-")
		if len(patterns) != 2 {
			return codeRange{}, fmt.Errorf("invalid pattern %s, more than one '-' exists", pattern)
		}
		start, err := strconv.Atoi(patterns[0])
		if err != nil {
			return codeRange{}, fmt.Errorf("failed to parse start code %s from pattern %s to int, %w", patterns[0], pattern, err)
		}
		end, err := strconv.Atoi(patterns[1])
		if err != nil {
			return codeRange{}, fmt.Errorf("failed to prase start code %s from pattern %s to int, %w", patterns[1], pattern, err)
		}
		return codeRange{start: int32(start), end: int32(end)}, nil
	}
	// specific code
	code, err := strconv.Atoi(pattern)
	if err != nil {
		return codeRange{}, fmt.Errorf("failed to compile pattern %s, %w", pattern, err)
	}

	return codeRange{
		start: int32(code),
		end:   int32(code),
	}, nil
}
