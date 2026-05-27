/*
Copyright 2025 The Dapr Authors
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

package errors

import (
	"errors"
	"strings"
)

const stalledMessage = "actor is stalled"

type stalled struct{}

func NewStalled() error {
	return &stalled{}
}

func (s *stalled) Error() string {
	return stalledMessage
}

var st = new(new(stalled))

// IsStalled reports whether err is, or wraps, a stalled actor error.
// Falls back to a message-suffix match so callers that receive the error
// flattened to a wire string (e.g. across a remote actor invocation) still
// detect it.
func IsStalled(err error) bool {
	if err == nil {
		return false
	}
	if errors.As(err, st) {
		return true
	}
	return strings.HasSuffix(err.Error(), stalledMessage)
}
