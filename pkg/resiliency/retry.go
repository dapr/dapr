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
	"regexp"
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
	*retry.Config
	*StatusCodeFilter
}

func NewRetry(retryConfig *retry.Config, statusCodeFilter *StatusCodeFilter) *Retry {
	return &Retry{
		Config:           retryConfig,
		StatusCodeFilter: statusCodeFilter,
	}
}

type StatusCodeFilter struct {
	retryOnPatterns  []*regexp.Regexp
	ignoreOnPatterns []*regexp.Regexp
	defaultRetry     bool
}

func NewStatusCodeFilter() *StatusCodeFilter {
	return &StatusCodeFilter{defaultRetry: true}
}

func ParseStatusCodeFilter(retryOnPatterns []string, ignoreOnPatterns []string) (*StatusCodeFilter, error) {
	if len(retryOnPatterns) != 0 && len(ignoreOnPatterns) != 0 {
		return nil, errors.New("retryOn and ignoreOn cannot be used together")
	}
	filter := NewStatusCodeFilter()
	if err := filter.ParseRetryOnList(retryOnPatterns); err != nil {
		return nil, err
	}
	if err := filter.ParseIgnoreOnList(ignoreOnPatterns); err != nil {
		return nil, err
	}
	return filter, nil
}

func (f *StatusCodeFilter) ParseRetryOnList(patterns []string) error {
	if len(patterns) == 0 {
		return nil
	}
	regexpPatterns, err := f.parsePatterns(patterns)
	if err != nil {
		return err
	}
	f.retryOnPatterns = regexpPatterns
	f.defaultRetry = false
	return nil
}

func (f *StatusCodeFilter) parsePatterns(patterns []string) ([]*regexp.Regexp, error) {
	regexpPatterns := make([]*regexp.Regexp, len(patterns))
	for i, pattern := range patterns {
		regexpPattern, err := compilePattern(pattern)
		if err != nil {
			return nil, err
		}
		regexpPatterns[i] = regexpPattern
	}
	return regexpPatterns, nil
}

func (f *StatusCodeFilter) ParseIgnoreOnList(patterns []string) error {
	if len(patterns) == 0 {
		return nil
	}
	regexpPatterns, err := f.parsePatterns(patterns)
	if err != nil {
		return err
	}
	f.ignoreOnPatterns = regexpPatterns
	f.defaultRetry = true
	return nil
}

func (f *StatusCodeFilter) StatusCodeNeedRetry(statusCode int32) bool {
	statusCodeStr := strconv.Itoa(int(statusCode))

	// Check retriable codes (allowlist)
	for _, pattern := range f.retryOnPatterns {
		if pattern.MatchString(statusCodeStr) {
			return true
		}
	}

	// Check ignored codes (denylist)
	for _, pattern := range f.ignoreOnPatterns {
		if pattern.MatchString(statusCodeStr) {
			return false
		}
	}

	return f.defaultRetry
}

func compilePattern(pattern string) (*regexp.Regexp, error) {
	// Convert wildcard patterns like "4**", "40*" to a regex pattern
	if strings.Contains(pattern, "*") {
		pattern = strings.ReplaceAll(pattern, "*", "[0-9]")
	}
	// Compile the regex pattern
	compiledPattern, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to compile pattern %s, %w", pattern, err)
	}

	return compiledPattern, nil
}
