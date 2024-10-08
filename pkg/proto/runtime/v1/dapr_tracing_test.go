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

package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
)

// Tests that all requests messages include the "AppendSpanAttribute" method.
func TestRequestsIncludeAppendSpanAttribute(t *testing.T) {
	fset := token.NewFileSet()

	// First, get the list of all structs whose name ends in "Request" from the "dapr.pb.go" file
	node, err := parser.ParseFile(fset, "dapr.pb.go", nil, 0)
	require.NoError(t, err)

	allRequests := make([]string, 0)
	for _, decl := range node.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}

		for _, spec := range genDecl.Specs {
			// Get structs only
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			_, ok = typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}

			// Only get structs whose name ends in "Request"
			if strings.HasSuffix(typeSpec.Name.Name, "Request") {
				allRequests = append(allRequests, typeSpec.Name.Name)
			}
		}
	}

	// Now, check that all the structs that were found implement the "AppendSpanAttributes" method in the "dapr_tracing.go" file
	node, err = parser.ParseFile(fset, "dapr_tracing.go", nil, 0)
	require.NoError(t, err)

	haveMethods := make([]string, 0, len(allRequests))
	for _, decl := range node.Decls {
		// Get all functions
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		// Filters for functions that:
		// - Have a receiver (i.e. are properties of an object)
		// - Are named "AppendSpanAttributes"
		// - Accept 2 arguments of type (string, map[string]string)
		// - Have no returned values
		if funcDecl.Name == nil || funcDecl.Name.Name != "AppendSpanAttributes" ||
			funcDecl.Type == nil || funcDecl.Recv == nil {
			continue
		}

		typ := funcDecl.Type
		if typ.Params == nil || len(typ.Params.List) != 2 || typ.Results != nil {
			continue
		}

		params := typ.Params.List

		// params[0] must be a string
		identType, ok := params[0].Type.(*ast.Ident)
		if !ok || identType.Name != "string" {
			continue
		}

		// params[1] must be a map[string]string
		mapType, ok := params[1].Type.(*ast.MapType)
		if !ok {
			continue
		}
		keyType, ok := mapType.Key.(*ast.Ident)
		if !ok || keyType.Name != "string" {
			continue
		}
		valueType, ok := mapType.Value.(*ast.Ident)
		if !ok || valueType.Name != "string" {
			continue
		}

		// Check the receiver
		recv := funcDecl.Recv.List
		if len(recv) == 0 {
			continue
		}
		starExpr, ok := recv[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		ident, ok := starExpr.X.(*ast.Ident)
		if !ok {
			continue
		}

		// We have the name; if it ends in "Request", we are golden
		if strings.HasSuffix(ident.Name, "Request") {
			haveMethods = append(haveMethods, ident.Name)
		}
	}

	// Compare the lists
	slices.Sort(allRequests)
	slices.Sort(haveMethods)

	assert.Equal(t, allRequests, haveMethods, "Some Request structs are missing the `AppendSpanAttributes(rpcMethod string, m map[string]string)` method")
}
