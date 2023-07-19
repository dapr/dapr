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

package annotations_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"

	"github.com/dapr/dapr/pkg/injector/patcher"
)

// This test makes sure that the SidecarConfig struct contains all and only the annotations defined as constants in the annotations package.
func TestAnnotationCompletness(t *testing.T) {
	annotationsPkg := []string{}
	annotationsStruct := []string{}

	// Load the annotations defined as constants in annotations.go, whose name starts with Key*
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "annotations.go", nil, 0)
	require.NoError(t, err)
	require.NotNil(t, node)
	require.Len(t, node.Decls, 1)

	for _, decl := range node.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl == nil || genDecl.Tok != token.CONST {
			continue
		}

		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || valueSpec == nil {
				continue
			}

			for _, name := range valueSpec.Names {
				if !strings.HasPrefix(name.Name, "Key") || len(valueSpec.Values) != 1 {
					continue
				}

				lit := valueSpec.Values[0].(*ast.BasicLit)
				if lit.Kind.String() != "STRING" {
					continue
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				annotationsPkg = append(annotationsPkg, val)
			}
		}
	}

	// Load the annotations listed as properties in SidecarConfig
	p := patcher.SidecarConfig{}
	pt := reflect.TypeOf(p)
	for i := 0; i < pt.NumField(); i++ {
		field := pt.Field(i)
		an := field.Tag.Get("annotation")
		if an != "" {
			annotationsStruct = append(annotationsStruct, an)
		}
	}

	// Sort so order doesn't matter
	slices.Sort(annotationsPkg)
	slices.Sort(annotationsStruct)

	// Check for completeness
	require.EqualValues(t, annotationsPkg, annotationsStruct)
}
