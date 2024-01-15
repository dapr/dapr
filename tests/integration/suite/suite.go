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
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
)

var cases []Case

// Case is a test case for the integration test suite.
type Case interface {
	Setup(*testing.T) []framework.Option
	Run(*testing.T, context.Context)
}

// NamedCase is a test case with a searchable name.
type NamedCase struct {
	name string
	Case
}

// Name returns the name of the test case.
func (c NamedCase) Name() string {
	return c.name
}

// Register registers a test case.
func Register(c Case) {
	cases = append(cases, c)
}

// All returns all registered test cases.
// The returned slice is sorted by name.
func All(t *testing.T) []NamedCase {
	all := make([]NamedCase, len(cases))
	for i, tcase := range cases {
		tof := reflect.TypeOf(tcase).Elem()
		_, aft, ok := strings.Cut(tof.PkgPath(), "tests/integration/suite/")
		require.True(t, ok)
		name := aft + "/" + tof.Name()
		all[i] = NamedCase{name, tcase}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].name < all[j].name
	})

	return all
}
