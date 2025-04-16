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

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_Expressions(t *testing.T) {
	tests := map[string]struct {
		filter *rtv1.SubscribeActorEventRequestFilterAlpha1
		event  map[string]any
		exp    bool
	}{
		"Exact match passes": {
			filter: wrapExact(newExpr("type", "created")),
			event:  map[string]any{"type": "created"},
			exp:    true,
		},
		"Exact match fails": {
			filter: wrapExact(newExpr("type", "deleted")),
			event:  map[string]any{"type": "created"},
			exp:    false,
		},
		"Multiple Exact expressions all match": {
			filter: wrapExact(
				newExpr("type", "created"),
				newExpr("source", "svc-a"),
			),
			event: map[string]any{
				"type":   "created",
				"source": "svc-a",
			},
			exp: true,
		},
		"Multiple Exact expressions, one fails": {
			filter: wrapExact(
				newExpr("type", "created"),
				newExpr("source", "svc-b"),
			),
			event: map[string]any{
				"type":   "created",
				"source": "svc-x",
			},
			exp: false,
		},
		"Multiple Prefix expressions all match": {
			filter: wrapPrefix(
				newExpr("type", "cr"),
				newExpr("source", "svc"),
			),
			event: map[string]any{
				"type":   "created",
				"source": "svc-a",
			},
			exp: true,
		},
		"Multiple Prefix expressions, one fails": {
			filter: wrapPrefix(
				newExpr("type", "cr"),
				newExpr("source", "api"),
			),
			event: map[string]any{
				"type":   "created",
				"source": "svc-a",
			},
			exp: false,
		},
		"Multiple Suffix expressions all match": {
			filter: wrapSuffix(
				newExpr("type", "ted"),
				newExpr("source", "c-a"),
			),
			event: map[string]any{
				"type":   "created",
				"source": "svc-a",
			},
			exp: true,
		},
		"Multiple Suffix expressions, one fails": {
			filter: wrapSuffix(
				newExpr("type", "ted"),
				newExpr("source", "bad"),
			),
			event: map[string]any{
				"type":   "created",
				"source": "svc-a",
			},
			exp: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fn, err := parse(test.filter)
			require.NoError(t, err)
			assert.Equal(t, test.exp, fn(test.event))
		})
	}
}

func Test_Parse_NestedCombinations(t *testing.T) {
	tests := map[string]struct {
		filter *rtv1.SubscribeActorEventRequestFilterAlpha1
		event  map[string]any
		exp    bool
	}{
		"All (AND) filters pass": {
			filter: wrapAll(
				wrapExact(newExpr("a", "1")),
				wrapPrefix(newExpr("b", "prefix")),
			),
			event: map[string]any{"a": "1", "b": "prefixVal"},
			exp:   true,
		},
		"All (AND) filters fail": {
			filter: wrapAll(
				wrapExact(newExpr("a", "1")),
				wrapPrefix(newExpr("b", "prefix")),
			),
			event: map[string]any{"a": "1", "b": "val"},
			exp:   false,
		},
		"Any (OR) filters one passes": {
			filter: wrapAny(
				wrapExact(newExpr("a", "nope")),
				wrapSuffix(newExpr("b", "end")),
			),
			event: map[string]any{"a": "wrong", "b": "match-end"},
			exp:   true,
		},
		"None passes (all fail)": {
			filter: wrapNone(
				wrapAll(
					wrapExact(newExpr("a", "x")),
					wrapSuffix(newExpr("b", "y")),
				),
			),
			event: map[string]any{"a": "no", "b": "nope"},
			exp:   true,
		},
		"None fails (all matches)": {
			filter: wrapNone(
				wrapAll(
					wrapExact(newExpr("a", "x")),
					wrapSuffix(newExpr("b", "z")),
				),
			),
			event: map[string]any{"a": "x", "b": "match-z"},
			exp:   false,
		},
		"Nested All within Any (OR of AND)": {
			filter: wrapAny(
				wrapAll(
					wrapExact(newExpr("x", "1")),
					wrapExact(newExpr("y", "2")),
				),
				wrapExact(newExpr("z", "ok")),
			),
			event: map[string]any{"x": "1", "y": "2"},
			exp:   true,
		},
		"Deeply nested composite fails": {
			filter: wrapAll(
				wrapAny(
					wrapExact(newExpr("a", "1")),
					wrapExact(newExpr("b", "2")),
				),
				wrapNone(wrapExact(newExpr("c", "block"))),
			),
			event: map[string]any{"a": "1", "c": "block"},
			exp:   false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fn, err := parse(test.filter)
			require.NoError(t, err)
			assert.Equal(t, test.exp, fn(test.event))
		})
	}
}

func Test_ParseExpressionValidation(t *testing.T) {
	t.Run("missing attribute returns error", func(t *testing.T) {
		_, err := parseExpression(&rtv1.SubscribeActorEventRequestExpressionAlpha1{
			AttributePath: nil,
			Value:         "val",
		}, opExact)
		assert.Error(t, err)
	})

	t.Run("missing value returns error", func(t *testing.T) {
		_, err := parseExpression(&rtv1.SubscribeActorEventRequestExpressionAlpha1{
			AttributePath: []string{"attr"},
			Value:         "",
		}, opExact)
		assert.Error(t, err)
	})
}

func Test_ParseFilters_Validation(t *testing.T) {
	t.Run("empty list returns error", func(t *testing.T) {
		_, err := parseFilters([]*rtv1.SubscribeActorEventRequestFilterAlpha1{}, filterAnd)
		assert.Error(t, err)
	})
}

func Test_ParseNot_Validation(t *testing.T) {
	t.Run("more than one filter returns error", func(t *testing.T) {
		_, err := parseNot([]*rtv1.SubscribeActorEventRequestFilterAlpha1{
			wrapExact(newExpr("x", "1")),
			wrapExact(newExpr("y", "2")),
		})
		assert.Error(t, err)
	})

	t.Run("negates correctly", func(t *testing.T) {
		fn, err := parseNot([]*rtv1.SubscribeActorEventRequestFilterAlpha1{
			wrapExact(newExpr("x", "block")),
		})
		require.NoError(t, err)
		assert.False(t, fn(map[string]any{"x": "block"}))
		assert.True(t, fn(map[string]any{"x": "other"}))
	})
}

func Test_Parse_NonStringAttributeValues(t *testing.T) {
	tests := map[string]struct {
		filter *rtv1.SubscribeActorEventRequestFilterAlpha1
		event  map[string]any
		exp    bool
	}{
		"Exact match with int vs string fails": {
			filter: wrapExact(newExpr("count", "42")),
			event:  map[string]any{"count": 42}, // int, not string
			exp:    false,
		},
		"Prefix match with int value returns false": {
			filter: wrapPrefix(newExpr("count", "4")),
			event:  map[string]any{"count": 42}, // not a string
			exp:    false,
		},
		"Suffix match with float value returns false": {
			filter: wrapSuffix(newExpr("pi", ".14")),
			event:  map[string]any{"pi": 3.14}, // float64
			exp:    false,
		},
		"Prefix match with bool value returns false": {
			filter: wrapPrefix(newExpr("enabled", "tru")),
			event:  map[string]any{"enabled": true},
			exp:    false,
		},
		"Exact match with bool string works": {
			filter: wrapExact(newExpr("enabled", "true")),
			event:  map[string]any{"enabled": "true"},
			exp:    true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fn, err := parse(test.filter)
			require.NoError(t, err)
			assert.Equal(t, test.exp, fn(test.event))
		})
	}
}

func Test_Parse_NestedAttributePaths(t *testing.T) {
	tests := map[string]struct {
		filter *rtv1.SubscribeActorEventRequestFilterAlpha1
		event  map[string]any
		exp    bool
	}{
		"Nested exact match succeeds": {
			filter: wrapExact(&rtv1.SubscribeActorEventRequestExpressionAlpha1{
				AttributePath: []string{"meta", "env"},
				Value:         "prod",
			}),
			event: map[string]any{
				"meta": map[string]any{
					"env": "prod",
				},
			},
			exp: true,
		},
		"Nested exact match fails": {
			filter: wrapExact(&rtv1.SubscribeActorEventRequestExpressionAlpha1{
				AttributePath: []string{"meta", "env"},
				Value:         "prod",
			}),
			event: map[string]any{
				"meta": map[string]any{
					"env": "dev",
				},
			},
			exp: false,
		},
		"Path not found": {
			filter: wrapExact(&rtv1.SubscribeActorEventRequestExpressionAlpha1{
				AttributePath: []string{"meta", "region"},
				Value:         "us-east",
			}),
			event: map[string]any{
				"meta": map[string]any{
					"env": "prod",
				},
			},
			exp: false,
		},
		"Path segment is not a map": {
			filter: wrapExact(&rtv1.SubscribeActorEventRequestExpressionAlpha1{
				AttributePath: []string{"meta", "env", "region"},
				Value:         "us-west",
			}),
			event: map[string]any{
				"meta": map[string]any{
					"env": "prod", // env is string, not map
				},
			},
			exp: false,
		},
		"Nested prefix match works": {
			filter: wrapPrefix(&rtv1.SubscribeActorEventRequestExpressionAlpha1{
				AttributePath: []string{"meta", "env"},
				Value:         "pr",
			}),
			event: map[string]any{
				"meta": map[string]any{
					"env": "prod",
				},
			},
			exp: true,
		},
		"Nested suffix match fails on wrong type": {
			filter: wrapSuffix(&rtv1.SubscribeActorEventRequestExpressionAlpha1{
				AttributePath: []string{"meta", "count"},
				Value:         "42",
			}),
			event: map[string]any{
				"meta": map[string]any{
					"count": 42, // int, not string
				},
			},
			exp: false,
		},
		"Exact match passes with path": {
			filter: wrapExact(&rtv1.SubscribeActorEventRequestExpressionAlpha1{
				AttributePath: []string{"data", "type"},
				Value:         "created",
			}),
			event: map[string]any{
				"data": map[string]any{"type": "created"},
			},
			exp: true,
		},
		"Exact match fails with path": {
			filter: wrapExact(&rtv1.SubscribeActorEventRequestExpressionAlpha1{
				AttributePath: []string{"data", "type"},
				Value:         "deleted",
			}),
			event: map[string]any{
				"data": map[string]any{"type": "created"},
			},
			exp: false,
		},
		"Multiple Exact path expressions all match": {
			filter: wrapExact(
				&rtv1.SubscribeActorEventRequestExpressionAlpha1{
					AttributePath: []string{"data", "type"},
					Value:         "created",
				},
				&rtv1.SubscribeActorEventRequestExpressionAlpha1{
					AttributePath: []string{"meta", "source"},
					Value:         "svc-a",
				},
			),
			event: map[string]any{
				"data": map[string]any{"type": "created"},
				"meta": map[string]any{"source": "svc-a"},
			},
			exp: true,
		},
		"Multiple Exact path expressions, one fails": {
			filter: wrapExact(
				&rtv1.SubscribeActorEventRequestExpressionAlpha1{
					AttributePath: []string{"data", "type"},
					Value:         "created",
				},
				&rtv1.SubscribeActorEventRequestExpressionAlpha1{
					AttributePath: []string{"meta", "source"},
					Value:         "svc-x",
				},
			),
			event: map[string]any{
				"data": map[string]any{"type": "created"},
				"meta": map[string]any{"source": "svc-a"},
			},
			exp: false,
		},

		"Multiple Prefix path expressions all match": {
			filter: wrapPrefix(
				&rtv1.SubscribeActorEventRequestExpressionAlpha1{
					AttributePath: []string{"data", "type"},
					Value:         "cr",
				},
				&rtv1.SubscribeActorEventRequestExpressionAlpha1{
					AttributePath: []string{"meta", "source"},
					Value:         "svc",
				},
			),
			event: map[string]any{
				"data": map[string]any{"type": "created"},
				"meta": map[string]any{"source": "svc-a"},
			},
			exp: true,
		},
		"Multiple Prefix path expressions, one fails": {
			filter: wrapPrefix(
				&rtv1.SubscribeActorEventRequestExpressionAlpha1{
					AttributePath: []string{"data", "type"},
					Value:         "cr",
				},
				&rtv1.SubscribeActorEventRequestExpressionAlpha1{
					AttributePath: []string{"meta", "source"},
					Value:         "api",
				},
			),
			event: map[string]any{
				"data": map[string]any{"type": "created"},
				"meta": map[string]any{"source": "svc-a"},
			},
			exp: false,
		},

		"Multiple Suffix path expressions all match": {
			filter: wrapSuffix(
				&rtv1.SubscribeActorEventRequestExpressionAlpha1{
					AttributePath: []string{"data", "type"},
					Value:         "ted",
				},
				&rtv1.SubscribeActorEventRequestExpressionAlpha1{
					AttributePath: []string{"meta", "source"},
					Value:         "c-a",
				},
			),
			event: map[string]any{
				"data": map[string]any{"type": "created"},
				"meta": map[string]any{"source": "svc-a"},
			},
			exp: true,
		},
		"Multiple Suffix path expressions, one fails": {
			filter: wrapSuffix(
				&rtv1.SubscribeActorEventRequestExpressionAlpha1{
					AttributePath: []string{"data", "type"},
					Value:         "ted",
				},
				&rtv1.SubscribeActorEventRequestExpressionAlpha1{
					AttributePath: []string{"meta", "source"},
					Value:         "bad",
				},
			),
			event: map[string]any{
				"data": map[string]any{"type": "created"},
				"meta": map[string]any{"source": "svc-a"},
			},
			exp: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fn, err := parse(test.filter)
			require.NoError(t, err)
			assert.Equal(t, test.exp, fn(test.event))
		})
	}
}

func newExpr(attr, val string) *rtv1.SubscribeActorEventRequestExpressionAlpha1 {
	return &rtv1.SubscribeActorEventRequestExpressionAlpha1{
		AttributePath: []string{attr},
		Value:         val,
	}
}

func wrapExact(exprs ...*rtv1.SubscribeActorEventRequestExpressionAlpha1) *rtv1.SubscribeActorEventRequestFilterAlpha1 {
	return &rtv1.SubscribeActorEventRequestFilterAlpha1{
		Filter: &rtv1.SubscribeActorEventRequestFilterAlpha1_Exact{
			Exact: &rtv1.SubscribeActorEventRequestFilterExactAlpha1{Expression: exprs},
		},
	}
}

func wrapPrefix(exprs ...*rtv1.SubscribeActorEventRequestExpressionAlpha1) *rtv1.SubscribeActorEventRequestFilterAlpha1 {
	return &rtv1.SubscribeActorEventRequestFilterAlpha1{
		Filter: &rtv1.SubscribeActorEventRequestFilterAlpha1_Prefix{
			Prefix: &rtv1.SubscribeActorEventRequestFilterPrefixAlpha1{Expression: exprs},
		},
	}
}

func wrapSuffix(exprs ...*rtv1.SubscribeActorEventRequestExpressionAlpha1) *rtv1.SubscribeActorEventRequestFilterAlpha1 {
	return &rtv1.SubscribeActorEventRequestFilterAlpha1{
		Filter: &rtv1.SubscribeActorEventRequestFilterAlpha1_Suffix{
			Suffix: &rtv1.SubscribeActorEventRequestFilterSuffixAlpha1{Expression: exprs},
		},
	}
}

func wrapAll(filters ...*rtv1.SubscribeActorEventRequestFilterAlpha1) *rtv1.SubscribeActorEventRequestFilterAlpha1 {
	return &rtv1.SubscribeActorEventRequestFilterAlpha1{
		Filter: &rtv1.SubscribeActorEventRequestFilterAlpha1_All{
			All: &rtv1.SubscribeActorEventRequestFilterAllAlpha1{Filters: filters},
		},
	}
}

func wrapAny(filters ...*rtv1.SubscribeActorEventRequestFilterAlpha1) *rtv1.SubscribeActorEventRequestFilterAlpha1 {
	return &rtv1.SubscribeActorEventRequestFilterAlpha1{
		Filter: &rtv1.SubscribeActorEventRequestFilterAlpha1_Any{
			Any: &rtv1.SubscribeActorEventRequestFilterAnyAlpha1{Filters: filters},
		},
	}
}

func wrapNone(filters ...*rtv1.SubscribeActorEventRequestFilterAlpha1) *rtv1.SubscribeActorEventRequestFilterAlpha1 {
	return &rtv1.SubscribeActorEventRequestFilterAlpha1{
		Filter: &rtv1.SubscribeActorEventRequestFilterAlpha1_None{
			None: &rtv1.SubscribeActorEventRequestFilterNoneAlpha1{Filters: filters},
		},
	}
}
