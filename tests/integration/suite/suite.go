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

package suite

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
)

var cases []Case

// Case is a test case for the integration test suite.
type Case interface {
	Setup(*testing.T) []framework.Option
	Run(*testing.T, context.Context)
}

// Register registers a test case.
func Register(c Case) {
	cases = append(cases, c)
}

// All returns all registered test cases.
func All() []Case {
	return cases
}
