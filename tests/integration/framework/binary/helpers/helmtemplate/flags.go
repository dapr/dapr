// section copied from: https://github.com/helm/helm/blob/2feac15cc3252c97c997be2ced1ab8afe314b429/cmd/helm/flags.go
/*
Copyright The Helm Authors.

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

package main

import (
	"github.com/spf13/pflag"
	"helm.sh/helm/v3/pkg/cli/values"
)

func addValueOptionsFlags(f *pflag.FlagSet, v *values.Options) {
	f.StringSliceVarP(&v.ValueFiles, "values", "f", []string{}, "specify values in a YAML file or a URL (can specify multiple)")
	f.StringArrayVar(&v.Values, "set", []string{}, "set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	f.StringArrayVar(&v.StringValues, "set-string", []string{}, "set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	f.StringArrayVar(&v.FileValues, "set-file", []string{}, "set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)")
	f.StringArrayVar(&v.JSONValues, "set-json", []string{}, "set JSON values on the command line (can specify multiple or separate values with commas: key1=jsonval1,key2=jsonval2)")
	f.StringArrayVar(&v.LiteralValues, "set-literal", []string{}, "set a literal STRING value on the command line")
}
