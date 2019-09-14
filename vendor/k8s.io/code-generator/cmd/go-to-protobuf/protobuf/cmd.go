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

// go-to-protobuf generates a Protobuf IDL from a Go struct, respecting any
// existing IDL tags on the Go struct.
package protobuf

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	flag "github.com/spf13/pflag"
	"gonum.org/v1/gonum/graph"
	"gonum.org/v1/gonum/graph/simple"
	"gonum.org/v1/gonum/graph/topo"

	"k8s.io/code-generator/pkg/util"
	"k8s.io/gengo/args"
	"k8s.io/gengo/generator"
	"k8s.io/gengo/namer"
	"k8s.io/gengo/parser"
	"k8s.io/gengo/types"
)

type Generator struct {
	Common               args.GeneratorArgs
	APIMachineryPackages string
	Packages             string
	OutputBase           string
	VendorOutputBase     string
	ProtoImport          []string
	Conditional          string
	Clean                bool
	OnlyIDL              bool
	KeepGogoproto        bool
	SkipGeneratedRewrite bool
	DropEmbeddedFields   string
}

func New() *Generator {
	sourceTree := args.DefaultSourceTree()
	common := args.GeneratorArgs{
		OutputBase:       sourceTree,
		GoHeaderFilePath: filepath.Join(sourceTree, util.BoilerplatePath()),
	}
	defaultProtoImport := filepath.Join(sourceTree, "k8s.io", "kubernetes", "vendor", "github.com", "gogo", "protobuf", "protobuf")
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("Cannot get current directory.")
	}
	return &Generator{
		Common:           common,
		OutputBase:       sourceTree,
		VendorOutputBase: filepath.Join(cwd, "vendor"),
		ProtoImport:      []string{defaultProtoImport},
		APIMachineryPackages: strings.Join([]string{
			`+k8s.io/apimachinery/pkg/util/intstr`,
			`+k8s.io/apimachinery/pkg/api/resource`,
			`+k8s.io/apimachinery/pkg/runtime/schema`,
			`+k8s.io/apimachinery/pkg/runtime`,
			`k8s.io/apimachinery/pkg/apis/meta/v1`,
			`k8s.io/apimachinery/pkg/apis/meta/v1beta1`,
			`k8s.io/apimachinery/pkg/apis/testapigroup/v1`,
		}, ","),
		Packages:           "",
		DropEmbeddedFields: "k8s.io/apimachinery/pkg/apis/meta/v1.TypeMeta",
	}
}

func (g *Generator) BindFlags(flag *flag.FlagSet) {
	flag.StringVarP(&g.Common.GoHeaderFilePath, "go-header-file", "h", g.Common.GoHeaderFilePath, "File containing boilerplate header text. The string YEAR will be replaced with the current 4-digit year.")
	flag.BoolVar(&g.Common.VerifyOnly, "verify-only", g.Common.VerifyOnly, "If true, only verify existing output, do not write anything.")
	flag.StringVarP(&g.Packages, "packages", "p", g.Packages, "comma-separated list of directories to get input types from. Directories prefixed with '-' are not generated, directories prefixed with '+' only create types with explicit IDL instructions.")
	flag.StringVar(&g.APIMachineryPackages, "apimachinery-packages", g.APIMachineryPackages, "comma-separated list of directories to get apimachinery input types from which are needed by any API. Directories prefixed with '-' are not generated, directories prefixed with '+' only create types with explicit IDL instructions.")
	flag.StringVarP(&g.OutputBase, "output-base", "o", g.OutputBase, "Output base; defaults to $GOPATH/src/")
	flag.StringVar(&g.VendorOutputBase, "vendor-output-base", g.VendorOutputBase, "The vendor/ directory to look for packages in; defaults to $PWD/vendor/.")
	flag.StringSliceVar(&g.ProtoImport, "proto-import", g.ProtoImport, "The search path for the core protobuf .protos, required; defaults $GOPATH/src/k8s.io/kubernetes/vendor/github.com/gogo/protobuf/protobuf.")
	flag.StringVar(&g.Conditional, "conditional", g.Conditional, "An optional Golang build tag condition to add to the generated Go code")
	flag.BoolVar(&g.Clean, "clean", g.Clean, "If true, remove all generated files for the specified Packages.")
	flag.BoolVar(&g.OnlyIDL, "only-idl", g.OnlyIDL, "If true, only generate the IDL for each package.")
	flag.BoolVar(&g.KeepGogoproto, "keep-gogoproto", g.KeepGogoproto, "If true, the generated IDL will contain gogoprotobuf extensions which are normally removed")
	flag.BoolVar(&g.SkipGeneratedRewrite, "skip-generated-rewrite", g.SkipGeneratedRewrite, "If true, skip fixing up the generated.pb.go file (debugging only).")
	flag.StringVar(&g.DropEmbeddedFields, "drop-embedded-fields", g.DropEmbeddedFields, "Comma-delimited list of embedded Go types to omit from generated protobufs")
}

func Run(g *Generator) {
	if g.Common.VerifyOnly {
		g.OnlyIDL = true
		g.Clean = false
	}

	b := parser.New()
	b.AddBuildTags("proto")

	omitTypes := map[types.Name]struct{}{}
	for _, t := range strings.Split(g.DropEmbeddedFields, ",") {
		name := types.Name{}
		if i := strings.LastIndex(t, "."); i != -1 {
			name.Package, name.Name = t[:i], t[i+1:]
		} else {
			name.Name = t
		}
		if len(name.Name) == 0 {
			log.Fatalf("--drop-embedded-types requires names in the form of [GOPACKAGE.]TYPENAME: %v", t)
		}
		omitTypes[name] = struct{}{}
	}

	boilerplate, err := g.Common.LoadGoBoilerplate()
	if err != nil {
		log.Fatalf("Failed loading boilerplate (consider using the go-header-file flag): %v", err)
	}

	protobufNames := NewProtobufNamer()
	outputPackages := generator.Packages{}
	nonOutputPackages := map[string]struct{}{}

	var packages []string
	if len(g.APIMachineryPackages) != 0 {
		packages = append(packages, strings.Split(g.APIMachineryPackages, ",")...)
	}
	if len(g.Packages) != 0 {
		packages = append(packages, strings.Split(g.Packages, ",")...)
	}
	if len(packages) == 0 {
		log.Fatalf("Both apimachinery-packages and packages are empty. At least one package must be specified.")
	}

	for _, d := range packages {
		generateAllTypes, outputPackage := true, true
		switch {
		case strings.HasPrefix(d, "+"):
			d = d[1:]
			generateAllTypes = false
		case strings.HasPrefix(d, "-"):
			d = d[1:]
			outputPackage = false
		}
		name := protoSafePackage(d)
		parts := strings.SplitN(d, "=", 2)
		if len(parts) > 1 {
			d = parts[0]
			name = parts[1]
		}
		p := newProtobufPackage(d, name, generateAllTypes, omitTypes)
		header := append([]byte{}, boilerplate...)
		header = append(header, p.HeaderText...)
		p.HeaderText = header
		protobufNames.Add(p)
		if outputPackage {
			outputPackages = append(outputPackages, p)
		} else {
			nonOutputPackages[name] = struct{}{}
		}
	}

	if !g.Common.VerifyOnly {
		for _, p := range outputPackages {
			if err := p.(*protobufPackage).Clean(g.OutputBase); err != nil {
				log.Fatalf("Unable to clean package %s: %v", p.Name(), err)
			}
		}
	}

	if g.Clean {
		return
	}

	for _, p := range protobufNames.List() {
		if err := b.AddDir(p.Path()); err != nil {
			log.Fatalf("Unable to add directory %q: %v", p.Path(), err)
		}
	}

	c, err := generator.NewContext(
		b,
		namer.NameSystems{
			"public": namer.NewPublicNamer(3),
			"proto":  protobufNames,
		},
		"public",
	)
	if err != nil {
		log.Fatalf("Failed making a context: %v", err)
	}

	c.Verify = g.Common.VerifyOnly
	c.FileTypes["protoidl"] = NewProtoFile()

	// order package by imports, importees first
	deps := deps(c, protobufNames.packages)
	order, err := importOrder(deps)
	if err != nil {
		log.Fatalf("Failed to order packages by imports: %v", err)
	}
	topologicalPos := map[string]int{}
	for i, p := range order {
		topologicalPos[p] = i
	}
	sort.Sort(positionOrder{topologicalPos, protobufNames.packages})

	var vendoredOutputPackages, localOutputPackages generator.Packages
	for _, p := range protobufNames.packages {
		if _, ok := nonOutputPackages[p.Name()]; ok {
			// if we're not outputting the package, don't include it in either package list
			continue
		}
		p.Vendored = strings.Contains(c.Universe[p.PackagePath].SourcePath, "/vendor/")
		if p.Vendored {
			vendoredOutputPackages = append(vendoredOutputPackages, p)
		} else {
			localOutputPackages = append(localOutputPackages, p)
		}
	}

	if err := protobufNames.AssignTypesToPackages(c); err != nil {
		log.Fatalf("Failed to identify Common types: %v", err)
	}

	if err := c.ExecutePackages(g.VendorOutputBase, vendoredOutputPackages); err != nil {
		log.Fatalf("Failed executing vendor generator: %v", err)
	}
	if err := c.ExecutePackages(g.OutputBase, localOutputPackages); err != nil {
		log.Fatalf("Failed executing local generator: %v", err)
	}

	if g.OnlyIDL {
		return
	}

	if _, err := exec.LookPath("protoc"); err != nil {
		log.Fatalf("Unable to find 'protoc': %v", err)
	}

	searchArgs := []string{"-I", ".", "-I", g.OutputBase}
	if len(g.ProtoImport) != 0 {
		for _, s := range g.ProtoImport {
			searchArgs = append(searchArgs, "-I", s)
		}
	}
	args := append(searchArgs, fmt.Sprintf("--gogo_out=%s", g.OutputBase))

	buf := &bytes.Buffer{}
	if len(g.Conditional) > 0 {
		fmt.Fprintf(buf, "// +build %s\n\n", g.Conditional)
	}
	buf.Write(boilerplate)

	for _, outputPackage := range outputPackages {
		p := outputPackage.(*protobufPackage)

		path := filepath.Join(g.OutputBase, p.ImportPath())
		outputPath := filepath.Join(g.OutputBase, p.OutputPath())
		if p.Vendored {
			path = filepath.Join(g.VendorOutputBase, p.ImportPath())
			outputPath = filepath.Join(g.VendorOutputBase, p.OutputPath())
		}

		// generate the gogoprotobuf protoc
		cmd := exec.Command("protoc", append(args, path)...)
		out, err := cmd.CombinedOutput()
		if len(out) > 0 {
			log.Printf(string(out))
		}
		if err != nil {
			log.Println(strings.Join(cmd.Args, " "))
			log.Fatalf("Unable to generate protoc on %s: %v", p.PackageName, err)
		}

		if g.SkipGeneratedRewrite {
			continue
		}

		// alter the generated protobuf file to remove the generated types (but leave the serializers) and rewrite the
		// package statement to match the desired package name
		if err := RewriteGeneratedGogoProtobufFile(outputPath, p.ExtractGeneratedType, p.OptionalTypeName, buf.Bytes()); err != nil {
			log.Fatalf("Unable to rewrite generated %s: %v", outputPath, err)
		}

		// sort imports
		cmd = exec.Command("goimports", "-w", outputPath)
		out, err = cmd.CombinedOutput()
		if len(out) > 0 {
			log.Printf(string(out))
		}
		if err != nil {
			log.Println(strings.Join(cmd.Args, " "))
			log.Fatalf("Unable to rewrite imports for %s: %v", p.PackageName, err)
		}

		// format and simplify the generated file
		cmd = exec.Command("gofmt", "-s", "-w", outputPath)
		out, err = cmd.CombinedOutput()
		if len(out) > 0 {
			log.Printf(string(out))
		}
		if err != nil {
			log.Println(strings.Join(cmd.Args, " "))
			log.Fatalf("Unable to apply gofmt for %s: %v", p.PackageName, err)
		}
	}

	if g.SkipGeneratedRewrite {
		return
	}

	if !g.KeepGogoproto {
		// generate, but do so without gogoprotobuf extensions
		for _, outputPackage := range outputPackages {
			p := outputPackage.(*protobufPackage)
			p.OmitGogo = true
		}
		if err := c.ExecutePackages(g.VendorOutputBase, vendoredOutputPackages); err != nil {
			log.Fatalf("Failed executing vendor generator: %v", err)
		}
		if err := c.ExecutePackages(g.OutputBase, localOutputPackages); err != nil {
			log.Fatalf("Failed executing local generator: %v", err)
		}
	}

	for _, outputPackage := range outputPackages {
		p := outputPackage.(*protobufPackage)

		if len(p.StructTags) == 0 {
			continue
		}

		pattern := filepath.Join(g.OutputBase, p.PackagePath, "*.go")
		if p.Vendored {
			pattern = filepath.Join(g.VendorOutputBase, p.PackagePath, "*.go")
		}
		files, err := filepath.Glob(pattern)
		if err != nil {
			log.Fatalf("Can't glob pattern %q: %v", pattern, err)
		}

		for _, s := range files {
			if strings.HasSuffix(s, "_test.go") {
				continue
			}
			if err := RewriteTypesWithProtobufStructTags(s, p.StructTags); err != nil {
				log.Fatalf("Unable to rewrite with struct tags %s: %v", s, err)
			}
		}
	}
}

func deps(c *generator.Context, pkgs []*protobufPackage) map[string][]string {
	ret := map[string][]string{}
	for _, p := range pkgs {
		for _, d := range c.Universe[p.PackagePath].Imports {
			ret[p.PackagePath] = append(ret[p.PackagePath], d.Path)
		}
	}
	return ret
}

func importOrder(deps map[string][]string) ([]string, error) {
	nodes := map[string]graph.Node{}
	names := map[int64]string{}
	g := simple.NewDirectedGraph()
	for pkg, imports := range deps {
		for _, imp := range imports {
			if _, found := nodes[pkg]; !found {
				n := g.NewNode()
				g.AddNode(n)
				nodes[pkg] = n
				names[n.ID()] = pkg
			}
			if _, found := nodes[imp]; !found {
				n := g.NewNode()
				g.AddNode(n)
				nodes[imp] = n
				names[n.ID()] = imp
			}
			g.SetEdge(g.NewEdge(nodes[imp], nodes[pkg]))
		}
	}

	ret := []string{}
	sorted, err := topo.Sort(g)
	if err != nil {
		return nil, err
	}
	for _, n := range sorted {
		ret = append(ret, names[n.ID()])
		fmt.Println("topological order", names[n.ID()])
	}
	return ret, nil
}

type positionOrder struct {
	pos      map[string]int
	elements []*protobufPackage
}

func (o positionOrder) Len() int {
	return len(o.elements)
}

func (o positionOrder) Less(i, j int) bool {
	return o.pos[o.elements[i].PackagePath] < o.pos[o.elements[j].PackagePath]
}

func (o positionOrder) Swap(i, j int) {
	x := o.elements[i]
	o.elements[i] = o.elements[j]
	o.elements[j] = x
}
