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

package filter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ExpressionOps(t *testing.T) {
	tests := []struct {
		name  string
		op    expressionOp
		input any
		arg   string
		exp   bool
	}{
		{"Exact match", opExact, "test", "test", true},
		{"Exact mismatch", opExact, "test", "fail", false},
		{"Suffix match", opSuffix, "hello.go", ".go", true},
		{"Suffix mismatch", opSuffix, "hello.go", ".js", false},
		{"Suffix non-string", opSuffix, 123, "3", false},
		{"Prefix match", opPrefix, "hello.go", "hell", true},
		{"Prefix mismatch", opPrefix, "hello.go", "js", false},
		{"Prefix non-string", opPrefix, true, "tr", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.exp, test.op(test.input, test.arg))
		})
	}
}

func Test_MakeOp(t *testing.T) {
	tests := map[string]struct {
		field string
		value string
		op    expressionOp
		input map[string]any
		exp   bool
	}{
		"Exact match": {
			"name", "Alice", opExact,
			map[string]any{"name": "Alice"},
			true,
		},
		"Prefix match": {
			"name", "Al", opPrefix,
			map[string]any{"name": "Alice"},
			true,
		},
		"Suffix mismatch": {
			"name", "Bob", opSuffix,
			map[string]any{"name": "Alice"},
			false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fn := makeOp(test.field, test.value, test.op)
			assert.Equal(t, test.exp, fn(test.input))
		})
	}
}

func Test_FilterOps(t *testing.T) {
	alwaysTrue := func(map[string]any) bool { return true }
	alwaysFalse := func(map[string]any) bool { return false }

	tests := []struct {
		name string
		op   filterOp
		a    FilterFunc
		b    FilterFunc
		exp  bool
	}{
		{"AND true/true", filterAnd, alwaysTrue, alwaysTrue, true},
		{"AND true/false", filterAnd, alwaysTrue, alwaysFalse, false},
		{"OR false/false", filterOr, alwaysFalse, alwaysFalse, false},
		{"OR true/false", filterOr, alwaysTrue, alwaysFalse, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inp := make(map[string]any)
			assert.Equal(t, test.exp, test.op(test.a, test.b)(inp))
		})
	}
}

func Test_MakeFilter(t *testing.T) {
	isGo := makeOp("ext", ".go", opSuffix)
	isTest := makeOp("name", "_test", opSuffix)

	tests := map[string]struct {
		a     FilterFunc
		b     FilterFunc
		op    filterOp
		input map[string]any
		exp   bool
	}{
		"MakeFilter AND match": {
			isGo, isTest, filterAnd,
			map[string]any{"ext": "main.go", "name": "foo_test"},
			true,
		},
		"MakeFilter AND mismatch": {
			isGo, isTest, filterAnd,
			map[string]any{"ext": "main.go", "name": "main"},
			false,
		},
		"MakeFilter OR match": {
			isGo, isTest, filterOr,
			map[string]any{"ext": "main.go", "name": "main"},
			true,
		},
		"MakeFilter OR mismatch": {
			isGo, isTest, filterOr,
			map[string]any{"ext": "main.py", "name": "main"},
			false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fn := makeFilter(test.a, test.b, test.op)
			assert.Equal(t, test.exp, fn(test.input))
		})
	}
}

func Test_FilterNot(t *testing.T) {
	isGo := makeOp("ext", ".go", opSuffix)

	tests := map[string]struct {
		fn    FilterFunc
		input map[string]any
		exp   bool
	}{
		"NOT true becomes false": {
			filterNot(isGo),
			map[string]any{"ext": "main.go"},
			false,
		},
		"NOT false becomes true": {
			filterNot(isGo),
			map[string]any{"ext": "main.py"},
			true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, test.exp, test.fn(test.input))
		})
	}
}
