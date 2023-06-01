/*
Copyright 2021 The Dapr Authors
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
	"fmt"
)

type InitErrorKind string

const (
	InitFailure            InitErrorKind = "INIT_FAILURE"
	InitComponentFailure   InitErrorKind = "INIT_COMPONENT_FAILURE"
	CreateComponentFailure InitErrorKind = "CREATE_COMPONENT_FAILURE"
)

type InitError struct {
	err    error
	kind   InitErrorKind
	entity string
}

func (e *InitError) Error() string {
	if e.entity == "" {
		return fmt.Sprintf("[%s]: %s", e.kind, e.err)
	}

	return fmt.Sprintf("[%s]: initialization error occurred for %s: %s", e.kind, e.entity, e.err)
}

func (e *InitError) Unwrap() error {
	return e.err
}

// NewInit returns an InitError wrapping an existing context error.
func NewInit(kind InitErrorKind, entity string, err error) *InitError {
	return &InitError{
		err:    err,
		kind:   kind,
		entity: entity,
	}
}
