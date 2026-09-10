/*
Copyright 2026 The Dapr Authors
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

// Command controllergen is a minimal wrapper around sigs.k8s.io/controller-tools
// that exposes only the generators the integration tests need (crd and object).
// It is kept in its own module so the heavy controller-tools dependency tree does
// not leak into the main dapr module, mirroring the helmtemplate helper.
//
// It accepts the same raw marker arguments as the upstream controller-gen binary,
// e.g:
//
//	controllergen crd:crdVersions=v1 paths=./pkg/apis/... output:crd:artifacts:config=/tmp/crds
package main

import (
	"fmt"
	"os"

	"sigs.k8s.io/controller-tools/pkg/crd"
	"sigs.k8s.io/controller-tools/pkg/deepcopy"
	"sigs.k8s.io/controller-tools/pkg/genall"
	"sigs.k8s.io/controller-tools/pkg/markers"
)

var (
	allGenerators = map[string]genall.Generator{
		"crd":    crd.Generator{},
		"object": deepcopy.Generator{},
	}

	allOutputRules = map[string]genall.OutputRule{
		"dir":       genall.OutputToDirectory(""),
		"none":      genall.OutputToNothing,
		"stdout":    genall.OutputToStdout,
		"artifacts": genall.OutputArtifacts{},
	}

	optionsRegistry = &markers.Registry{}
)

func init() {
	for genName, gen := range allGenerators {
		defn := markers.Must(markers.MakeDefinition(genName, markers.DescribesPackage, gen))
		if err := optionsRegistry.Register(defn); err != nil {
			panic(err)
		}
		for ruleName, rule := range allOutputRules {
			ruleMarker := markers.Must(markers.MakeDefinition(fmt.Sprintf("output:%s:%s", genName, ruleName), markers.DescribesPackage, rule))
			if err := optionsRegistry.Register(ruleMarker); err != nil {
				panic(err)
			}
		}
	}

	for ruleName, rule := range allOutputRules {
		ruleMarker := markers.Must(markers.MakeDefinition("output:"+ruleName, markers.DescribesPackage, rule))
		if err := optionsRegistry.Register(ruleMarker); err != nil {
			panic(err)
		}
	}

	if err := genall.RegisterOptionsMarkers(optionsRegistry); err != nil {
		panic(err)
	}
}

func main() {
	rt, err := genall.FromOptions(optionsRegistry, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	if len(rt.Generators) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no generators specified")
		os.Exit(1)
	}

	if hadErrs := rt.Run(); hadErrs {
		fmt.Fprintln(os.Stderr, "Error: not all generators ran successfully")
		os.Exit(1)
	}
}
