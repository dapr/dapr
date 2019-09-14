/*
Copyright 2015 The Kubernetes Authors.

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

// Package generators has the generators for the set-gen utility.
package generators

import (
	"io"

	"k8s.io/gengo/args"
	"k8s.io/gengo/generator"
	"k8s.io/gengo/namer"
	"k8s.io/gengo/types"

	"k8s.io/klog"
)

// NameSystems returns the name system used by the generators in this package.
func NameSystems() namer.NameSystems {
	return namer.NameSystems{
		"public":  namer.NewPublicNamer(0),
		"private": namer.NewPrivateNamer(0),
		"raw":     namer.NewRawNamer("", nil),
	}
}

// DefaultNameSystem returns the default name system for ordering the types to be
// processed by the generators in this package.
func DefaultNameSystem() string {
	return "public"
}

// Packages makes the sets package definition.
func Packages(_ *generator.Context, arguments *args.GeneratorArgs) generator.Packages {
	boilerplate, err := arguments.LoadGoBoilerplate()
	if err != nil {
		klog.Fatalf("Failed loading boilerplate: %v", err)
	}

	return generator.Packages{&generator.DefaultPackage{
		PackageName: "sets",
		PackagePath: arguments.OutputPackagePath,
		HeaderText:  boilerplate,
		PackageDocumentation: []byte(
			`// Package sets has auto-generated set types.
`),
		// GeneratorFunc returns a list of generators. Each generator makes a
		// single file.
		GeneratorFunc: func(c *generator.Context) (generators []generator.Generator) {
			generators = []generator.Generator{
				// Always generate a "doc.go" file.
				generator.DefaultGen{OptionalName: "doc"},
				// Make a separate file for the Empty type, since it's shared by every type.
				generator.DefaultGen{
					OptionalName: "empty",
					OptionalBody: []byte(emptyTypeDecl),
				},
			}
			// Since we want a file per type that we generate a set for, we
			// have to provide a function for this.
			for _, t := range c.Order {
				generators = append(generators, &genSet{
					DefaultGen: generator.DefaultGen{
						// Use the privatized version of the
						// type name as the file name.
						//
						// TODO: make a namer that converts
						// camelCase to '-' separation for file
						// names?
						OptionalName: c.Namers["private"].Name(t),
					},
					outputPackage: arguments.OutputPackagePath,
					typeToMatch:   t,
					imports:       generator.NewImportTracker(),
				})
			}
			return generators
		},
		FilterFunc: func(c *generator.Context, t *types.Type) bool {
			// It would be reasonable to filter by the type's package here.
			// It might be necessary if your input directory has a big
			// import graph.
			switch t.Kind {
			case types.Map, types.Slice, types.Pointer:
				// These types can't be keys in a map.
				return false
			case types.Builtin:
				return true
			case types.Struct:
				// Only some structs can be keys in a map. This is triggered by the line
				// // +genset
				// or
				// // +genset=true
				return extractBoolTagOrDie("genset", t.CommentLines) == true
			}
			return false
		},
	}}
}

// genSet produces a file with a set for a single type.
type genSet struct {
	generator.DefaultGen
	outputPackage string
	typeToMatch   *types.Type
	imports       namer.ImportTracker
}

// Filter ignores all but one type because we're making a single file per type.
func (g *genSet) Filter(c *generator.Context, t *types.Type) bool { return t == g.typeToMatch }

func (g *genSet) Namers(c *generator.Context) namer.NameSystems {
	return namer.NameSystems{
		"raw": namer.NewRawNamer(g.outputPackage, g.imports),
	}
}

func (g *genSet) Imports(c *generator.Context) (imports []string) {
	return append(g.imports.ImportLines(), "reflect", "sort")
}

// args constructs arguments for templates. Usage:
// g.args(t, "key1", value1, "key2", value2, ...)
//
// 't' is loaded with the key 'type'.
//
// We could use t directly as the argument, but doing it this way makes it easy
// to mix in additional parameters. This feature is not used in this set
// generator, but is present as an example.
func (g *genSet) args(t *types.Type, kv ...interface{}) interface{} {
	m := map[interface{}]interface{}{"type": t}
	for i := 0; i < len(kv)/2; i++ {
		m[kv[i*2]] = kv[i*2+1]
	}
	return m
}

// GenerateType makes the body of a file implementing a set for type t.
func (g *genSet) GenerateType(c *generator.Context, t *types.Type, w io.Writer) error {
	sw := generator.NewSnippetWriter(w, c, "$", "$")
	sw.Do(setCode, g.args(t))
	sw.Do("func less$.type|public$(lhs, rhs $.type|raw$) bool {\n", g.args(t))
	g.lessBody(sw, t)
	sw.Do("}\n", g.args(t))
	return sw.Error()
}

func (g *genSet) lessBody(sw *generator.SnippetWriter, t *types.Type) {
	// TODO: make this recursive, handle pointers and multiple nested structs...
	switch t.Kind {
	case types.Struct:
		for _, m := range types.FlattenMembers(t.Members) {
			sw.Do("if lhs.$.Name$ < rhs.$.Name$ { return true }\n", m)
			sw.Do("if lhs.$.Name$ > rhs.$.Name$ { return false }\n", m)
		}
		sw.Do("return false\n", nil)
	default:
		sw.Do("return lhs < rhs\n", nil)
	}
}

// written to the "empty.go" file.
var emptyTypeDecl = `
// Empty is public since it is used by some internal API objects for conversions between external
// string arrays and internal sets, and conversion logic requires public types today.
type Empty struct{}
`

// Written for every type. If you've never used text/template before:
// $.type$ refers to the source type; |public means to
// call the function giving the public name, |raw the raw type name.
var setCode = `// sets.$.type|public$ is a set of $.type|raw$s, implemented via map[$.type|raw$]struct{} for minimal memory consumption.
type $.type|public$ map[$.type|raw$]Empty

// New$.type|public$ creates a $.type|public$ from a list of values.
func New$.type|public$(items ...$.type|raw$) $.type|public$ {
	ss := $.type|public${}
	ss.Insert(items...)
	return ss
}

// $.type|public$KeySet creates a $.type|public$ from a keys of a map[$.type|raw$](? extends interface{}).
// If the value passed in is not actually a map, this will panic.
func $.type|public$KeySet(theMap interface{}) $.type|public$ {
	v := reflect.ValueOf(theMap)
	ret := $.type|public${}

	for _, keyValue := range v.MapKeys() {
		ret.Insert(keyValue.Interface().($.type|raw$))
	}
	return ret
}

// Insert adds items to the set.
func (s $.type|public$) Insert(items ...$.type|raw$) $.type|public$ {
	for _, item := range items {
		s[item] = Empty{}
	}
	return s
}

// Delete removes all items from the set.
func (s $.type|public$) Delete(items ...$.type|raw$) $.type|public$ {
	for _, item := range items {
		delete(s, item)
	}
	return s
}

// Has returns true if and only if item is contained in the set.
func (s $.type|public$) Has(item $.type|raw$) bool {
	_, contained := s[item]
	return contained
}

// HasAll returns true if and only if all items are contained in the set.
func (s $.type|public$) HasAll(items ...$.type|raw$) bool {
	for _, item := range items {
		if !s.Has(item) {
			return false
		}
	}
	return true
}

// HasAny returns true if any items are contained in the set.
func (s $.type|public$) HasAny(items ...$.type|raw$) bool {
	for _, item := range items {
		if s.Has(item) {
			return true
		}
	}
	return false
}

// Difference returns a set of objects that are not in s2
// For example:
// s1 = {a1, a2, a3}
// s2 = {a1, a2, a4, a5}
// s1.Difference(s2) = {a3}
// s2.Difference(s1) = {a4, a5}
func (s $.type|public$) Difference(s2 $.type|public$) $.type|public$ {
	result := New$.type|public$()
	for key := range s {
		if !s2.Has(key) {
			result.Insert(key)
		}
	}
	return result
}

// Union returns a new set which includes items in either s1 or s2.
// For example:
// s1 = {a1, a2}
// s2 = {a3, a4}
// s1.Union(s2) = {a1, a2, a3, a4}
// s2.Union(s1) = {a1, a2, a3, a4}
func (s1 $.type|public$) Union(s2 $.type|public$) $.type|public$ {
	result := New$.type|public$()
	for key := range s1 {
		result.Insert(key)
	}
	for key := range s2 {
		result.Insert(key)
	}
	return result
}

// Intersection returns a new set which includes the item in BOTH s1 and s2
// For example:
// s1 = {a1, a2}
// s2 = {a2, a3}
// s1.Intersection(s2) = {a2}
func (s1 $.type|public$) Intersection(s2 $.type|public$) $.type|public$ {
	var walk, other $.type|public$
	result := New$.type|public$()
	if s1.Len() < s2.Len() {
		walk = s1
		other = s2
	} else {
		walk = s2
		other = s1
	}
	for key := range walk {
		if other.Has(key) {
			result.Insert(key)
		}
	}
	return result
}

// IsSuperset returns true if and only if s1 is a superset of s2.
func (s1 $.type|public$) IsSuperset(s2 $.type|public$) bool {
	for item := range s2 {
		if !s1.Has(item) {
			return false
		}
	}
	return true
}

// Equal returns true if and only if s1 is equal (as a set) to s2.
// Two sets are equal if their membership is identical.
// (In practice, this means same elements, order doesn't matter)
func (s1 $.type|public$) Equal(s2 $.type|public$) bool {
	return len(s1) == len(s2) && s1.IsSuperset(s2)
}

type sortableSliceOf$.type|public$ []$.type|raw$

func (s sortableSliceOf$.type|public$) Len() int { return len(s) }
func (s sortableSliceOf$.type|public$) Less(i, j int) bool { return less$.type|public$(s[i], s[j]) }
func (s sortableSliceOf$.type|public$) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// List returns the contents as a sorted $.type|raw$ slice.
func (s $.type|public$) List() []$.type|raw$ {
	res := make(sortableSliceOf$.type|public$, 0, len(s))
	for key := range s {
		res = append(res, key)
	}
	sort.Sort(res)
	return []$.type|raw$(res)
}

// UnsortedList returns the slice with contents in random order.
func (s $.type|public$) UnsortedList() []$.type|raw$ {
	res :=make([]$.type|raw$, 0, len(s))
	for key := range s {
		res = append(res, key)
	}
	return res
}

// Returns a single element from the set.
func (s $.type|public$) PopAny() ($.type|raw$, bool) {
	for key := range s {
		s.Delete(key)
		return key, true
	}
	var zeroValue $.type|raw$
	return zeroValue, false
}

// Len returns the size of the set.
func (s $.type|public$) Len() int {
	return len(s)
}

`
