package expr_test

import (
	"encoding/json"
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

func TestUnmarshalNull(t *testing.T) {
	var e expr.Expr
	err := json.Unmarshal([]byte("null"), &e)
	require.NoError(t, err)
	assert.Equal(t, "", e.Expr())
	assert.Equal(t, "", e.String())
}

func TestNilExpr(t *testing.T) {
	var e *expr.Expr
	assert.Equal(t, "", e.String())
	assert.NotPanics(t, func() {
		assert.Equal(t, "", e.Expr())
	})
	assert.NotPanics(t, func() {
		b, err := e.MarshalJSON()
		require.NoError(t, err)
		assert.Equal(t, "null", string(b))
	})
	assert.NotPanics(t, func() {
		r, err := e.Eval(nil)
		assert.Nil(t, r)
		require.ErrorContains(t, err, "cannot evaluate nil expression")
	})
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
