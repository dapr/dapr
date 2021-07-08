package expr

import (
	"encoding/json"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	expr_proto "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

type Expr struct {
	expr    string
	program cel.Program
}

func (e *Expr) DecodeString(value string) (err error) {
	var ast *cel.Ast
	var env *cel.Env

	_variables := [10]*expr_proto.Decl{}
	variables := _variables[:0]

	for {
		env, err = cel.NewEnv(cel.Declarations(variables...))
		if err != nil {
			return err
		}
		var iss *cel.Issues
		ast, iss = env.Compile(value)
		if iss.Err() != nil {
			for _, e := range iss.Errors() {
				if strings.HasPrefix(e.Message, "undeclared reference to '") {
					msg := e.Message[25:]
					msg = msg[0:strings.IndexRune(msg, '\'')]
					variables = append(variables, decls.NewVar(msg, decls.Any))
				} else {
					return iss.Err()
				}
			}
		} else {
			break
		}
	}
	prg, err := env.Program(ast)
	if err != nil {
		return err
	}

	*e = Expr{
		expr:    value,
		program: prg,
	}

	return nil
}

func (e *Expr) Eval(variables map[string]interface{}) (interface{}, error) {
	out, _, err := e.program.Eval(variables)
	if err != nil {
		return nil, err
	}

	return out.Value(), nil
}

func (e *Expr) Expr() string {
	return e.expr
}

func (e *Expr) String() string {
	return e.expr
}

func (e *Expr) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.expr)
}

func (e *Expr) UnmarshalJSON(b []byte) error {
	var code string
	if err := json.Unmarshal(b, &code); err != nil {
		return err
	}

	return e.DecodeString(code)
}
