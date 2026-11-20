package expr_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/expr"
)

func TestEval(t *testing.T) {
	var e expr.Expr
	code := `(has(input.test) && input.test == 1234) || (has(result.test) && result.test == 5678)`
	err := e.DecodeString(code)
	require.NoError(t, err)
	assert.Equal(t, code, e.String())
	result, err := e.Eval(map[string]any{
		"input": map[string]any{
			"test": 1234,
		},
		"result": map[string]any{
			"test": 5678,
		},
	})
	require.NoError(t, err)
	assert.True(t, result.(bool))
}

func TestJSONMarshal(t *testing.T) {
	var e expr.Expr
	exprBytes := []byte(`"(has(input.test) && input.test == 1234) || (has(result.test) && result.test == 5678)"`)
	err := e.UnmarshalJSON(exprBytes)
	require.NoError(t, err)
	assert.Equal(t, `(has(input.test) && input.test == 1234) || (has(result.test) && result.test == 5678)`, e.Expr())
	_, err = e.MarshalJSON()
	require.NoError(t, err)
}

func TestEmptyProgramNoPanic(t *testing.T) {
	var e expr.Expr
	r, err := e.Eval(map[string]any{})

	assert.Nil(t, r)
	require.Error(t, err)
}

func TestLeadingDot(t *testing.T) {
	tests := map[string]struct {
		code   string
		vars   map[string]any
		expect bool
	}{
		"leading dot on simple variable": {
			code:   ".data == 1234",
			vars:   map[string]any{"data": 1234},
			expect: true,
		},
		"leading dot on nested field": {
			code:   ".input.test == 1234",
			vars:   map[string]any{"input": map[string]any{"test": 1234}},
			expect: true,
		},
		"leading dot with has() check": {
			code:   "has(.input.test) && .input.test == 1234",
			vars:   map[string]any{"input": map[string]any{"test": 1234}},
			expect: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var e expr.Expr
			err := e.DecodeString(tc.code)
			require.NoError(t, err)
			res, err := e.Eval(tc.vars)
			require.NoError(t, err)
			assert.Equal(t, tc.expect, res)
		})
	}
}

func TestNoInfiniteLoopOnInvalidExpr(t *testing.T) {
	tests := []string{
		".",
		"..",
		"foo(bar)",
		"unknownFunc(input.test)",
		".startsWit('abc')",
	}
	for _, code := range tests {
		t.Run(code, func(t *testing.T) {
			var e expr.Expr
			err := e.DecodeString(code)
			require.Error(t, err)
		})
	}
}

var result any

func BenchmarkEval(b *testing.B) {
	var e expr.Expr
	err := e.DecodeString(`(has(input.test) && input.test == 1234) || (has(result.test) && result.test == 5678)`)
	require.NoError(b, err)
	data := map[string]any{
		"input": map[string]any{
			"test": 1234,
		},
		"result": map[string]any{
			"test": 5678,
		},
	}
	var r any
	for range b.N {
		r, _ = e.Eval(data)
	}
	result = r
}
