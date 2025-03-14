package daprd

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

type Flag struct {
	Description        string
	DefaultValue       string
	Deprecated         bool
	DeprecationMessage string
}

func ParseDefault(importPath, value string) (*string, error) {
	return nil, errors.New("not implemented")
}

// ParseFlags for a given filepath will return a map of flags with their respective metadata
func ParseFlags(filePath string) (*map[string]Flag, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, 0)
	if err != nil {
		return nil, err
	}

	flags := make(map[string]Flag)

	ast.Inspect(file, func(n ast.Node) bool {
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		selExpr, ok := callExpr.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		ident, ok := selExpr.X.(*ast.Ident)
		if !ok || ident.Name != "fs" {
			return true
		}

		funcName := selExpr.Sel.Name

		if funcName == "MarkDeprecated" {
			if len(callExpr.Args) != 2 {
				return true
			}

			flagName := getStringValue(callExpr.Args[0])
			depMessage := getStringValue(callExpr.Args[1])

			if flagName != "" {
				flagData := flags[flagName]
				flagData.Deprecated = true
				flagData.DeprecationMessage = depMessage
				flags[flagName] = flagData
			}
			return true
		}

		if !strings.HasSuffix(funcName, "Var") {
			return true
		}

		if len(callExpr.Args) != 4 {
			return true
		}

		flagName := getStringValue(callExpr.Args[1])
		defaultValue := getStringValueRaw(callExpr.Args[2], file.Scope)
		fmt.Println(callExpr.Args[2])
		description := getStringValue(callExpr.Args[3])

		if flagName != "" {
			flags[flagName] = Flag{
				Description:        description,
				DefaultValue:       defaultValue,
				Deprecated:         flags[flagName].Deprecated,
				DeprecationMessage: flags[flagName].DeprecationMessage,
			}
		}

		return true
	})

	fmt.Println("flags found:", len(flags))
	return &flags, nil
}

// getStringValueRaw extracts the string value from an ast.Expr
func getStringValueRaw(node ast.Expr, scope *ast.Scope) string {
	switch v := node.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			return v.Value
		}
	case *ast.Ident:
		if scope != nil {
			if obj := scope.Lookup(v.Name); obj != nil && obj.Kind == ast.Con {
				if spec, ok := obj.Decl.(*ast.ValueSpec); ok && len(spec.Values) > 0 {
					if lit, ok := spec.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						return lit.Value
					}
				}
			}
		}
		return v.Name
	case *ast.CallExpr:
		if fun, ok := v.Fun.(*ast.Ident); ok && fun.Name == "string" && len(v.Args) == 1 {
			argValue := getStringValueRaw(v.Args[0], scope)
			return argValue
		}
	case *ast.SelectorExpr:
		if ident, ok := v.X.(*ast.Ident); ok {
			if scope != nil {
				if obj := scope.Lookup(ident.Name); obj != nil && obj.Kind == ast.Pkg {
					if pkg, ok := obj.Data.(*ast.Object); ok {
						for name, obj := range pkg.Decl.(*ast.File).Scope.Objects {
							if name == v.Sel.Name && obj.Kind == ast.Con {
								if spec, ok := obj.Decl.(*ast.ValueSpec); ok && len(spec.Values) > 0 {
									if lit, ok := spec.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
										return lit.Value
									} else if id, ok := spec.Values[0].(*ast.Ident); ok {
										return getStringValueRaw(id, scope)
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return ""
}

// getStringValue returns the value without the quotation marks
func getStringValue(node ast.Expr) string {
	return strings.Trim(getStringValueRaw(node, nil), "\"")
}
