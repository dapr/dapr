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

type InitKind string

const (
	InitFailure            InitKind = "INIT_FAILURE"
	InitComponentFailure   InitKind = "INIT_COMPONENT_FAILURE"
	CreateComponentFailure InitKind = "CREATE_COMPONENT_FAILURE"
)

type Init struct {
	err    error
	kind   InitKind
	entity string
}

func (e *Init) Error() string {
	if e.entity == "" {
		return fmt.Sprintf("[%s]: %s", e.kind, e.err)
	}

	return fmt.Sprintf("[%s]: initialization error occurred for %s: %s", e.kind, e.entity, e.err)
}

func (e *Init) Unwrap() error {
	return e.err
}

// NewInit returns an Init wrapping an existing context error.
func NewInit(kind InitKind, entity string, err error) *Init {
	return &Init{
		err:    err,
		kind:   kind,
		entity: entity,
	}
}
