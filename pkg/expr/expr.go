package expr

import (
	"encoding/json"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	expr_proto "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

const missingVariableMessage = "undeclared reference to '"

type Expr struct {
	expr    string
	program cel.Program
}

func (e *Expr) DecodeString(value string) (err error) {
	var ast *cel.Ast
	var env *cel.Env

	variables := make([]*expr_proto.Decl, 0, 10)
	found := make(map[string]struct{}, 10)

	for {
		env, err = cel.NewEnv(cel.Declarations(variables...))
		if err != nil {
			return err
		}
		var iss *cel.Issues
		ast, iss = env.Compile(value)
		if iss.Err() != nil {
			for _, e := range iss.Errors() {
				if strings.HasPrefix(e.Message, missingVariableMessage) {
					msg := e.Message[len(missingVariableMessage):]
					msg = msg[0:strings.IndexRune(msg, '\'')]
					if _, exists := found[msg]; exists {
						continue
					}
					variables = append(variables, decls.NewVar(msg, decls.Any))
					found[msg] = struct{}{}
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
