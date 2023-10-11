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

package errors

type RetriableError struct {
	err error
}

func (e *RetriableError) Error() string {
	if e.err != nil {
		return "retriable error occurred: " + e.err.Error()
	}
	return "retriable error occurred"
}

func (e *RetriableError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.err
}

// NewRetriable returns a RetriableError wrapping an existing context error.
func NewRetriable(err error) *RetriableError {
	return &RetriableError{
		err: err,
	}
}
