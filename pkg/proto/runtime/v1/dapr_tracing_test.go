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
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
)

// Tests that all requests messages include the "AppendSpanAttribute" method.
func TestRequestsIncludeAppendSpanAttribute(t *testing.T) {
	fset := token.NewFileSet()

	var files []string
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.Name() != "appcallback.pb.go" && strings.HasSuffix(entry.Name(), ".pb.go") {
			files = append(files, entry.Name())
		}
	}

	allRequests := make([]string, 0)
	for _, file := range files {
		node, err := parser.ParseFile(fset, file, nil, 0)
		require.NoError(t, err)

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
				if strings.HasSuffix(typeSpec.Name.Name, "Request") || strings.HasSuffix(typeSpec.Name.Name, "RequestAlpha2") {
					allRequests = append(allRequests, typeSpec.Name.Name)
				}
			}
		}
	}

	// Now, check that all the structs that were found implement the "AppendSpanAttributes" method in the "dapr_tracing.go" file
	tracingNode, err := parser.ParseFile(fset, "dapr_tracing.go", nil, 0)
	require.NoError(t, err)

	haveMethods := make([]string, 0, len(allRequests))
	for _, decl := range tracingNode.Decls {
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

		// We have the name; if it ends in "Request" or "RequestAlpha2", we are golden
		if strings.HasSuffix(ident.Name, "Request") || strings.HasSuffix(ident.Name, "RequestAlpha2") {
			haveMethods = append(haveMethods, ident.Name)
		}
	}

	// Compare the lists
	slices.Sort(allRequests)
	slices.Sort(haveMethods)

	hasConversationRequest := false
	hasConversationRequestAlpha2 := false
	for _, req := range allRequests {
		if req == "ConversationRequest" {
			hasConversationRequest = true
		}
		if req == "ConversationRequestAlpha2" {
			hasConversationRequestAlpha2 = true
		}
	}
	assert.True(t, hasConversationRequest)
	assert.True(t, hasConversationRequestAlpha2)
	assert.Equal(t, allRequests, haveMethods, "Some Request structs are missing the `AppendSpanAttributes(rpcMethod string, m map[string]string)` method")
}

func TestSearchAndVectorSpanAttributes(t *testing.T) {
	for want, reqs := range map[string][]diagConsts.GrpcAppendSpanAttributesFn{
		diagConsts.SearchBuildingBlockType: {
			&CreateIndexRequestAlpha1{StoreName: "s"}, &GetIndexRequestAlpha1{StoreName: "s"},
			&ListIndexesRequestAlpha1{StoreName: "s"}, &DeleteIndexRequestAlpha1{StoreName: "s"},
			&IndexDocumentsRequestAlpha1{StoreName: "s"}, &GetDocumentsRequestAlpha1{StoreName: "s"},
			&DeleteDocumentsRequestAlpha1{StoreName: "s"}, &SearchRequestAlpha1{StoreName: "s"},
		},
		diagConsts.VectorBuildingBlockType: {
			&CreateCollectionRequestAlpha1{StoreName: "s"}, &GetCollectionRequestAlpha1{StoreName: "s"},
			&ListCollectionsRequestAlpha1{StoreName: "s"}, &DeleteCollectionRequestAlpha1{StoreName: "s"},
			&UpsertVectorsRequestAlpha1{StoreName: "s"}, &GetVectorsRequestAlpha1{StoreName: "s"},
			&DeleteVectorsRequestAlpha1{StoreName: "s"}, &QueryVectorsRequestAlpha1{StoreName: "s"},
			&BatchQueryVectorsRequestAlpha1{StoreName: "s"},
		},
	} {
		for _, req := range reqs {
			m := map[string]string{}
			req.AppendSpanAttributes("/dapr.proto.runtime.v1.Dapr/Method", m)
			assert.Equal(t, want, m[diagConsts.DBSystemSpanAttributeKey])
			assert.Equal(t, "s", m[diagConsts.DBNameSpanAttributeKey])
			assert.Equal(t, "/dapr.proto.runtime.v1.Dapr/Method", m[diagConsts.DBStatementSpanAttributeKey])
		}
	}
}
