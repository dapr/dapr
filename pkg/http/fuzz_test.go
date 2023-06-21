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

package http

import (
	"regexp"
	"testing"

	fuzz "github.com/AdamKorcz/go-fuzz-headers-1"

	"github.com/dapr/dapr/pkg/config"
)

func FuzzIsEndpointAllowed(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		ff := fuzz.NewConsumer(data)
		endpoint := Endpoint{}
		ff.GenerateStruct(&endpoint)
		if endpoint.Version == "" {
			return
		}
		if endpoint.Route == "" {
			return
		}
		rule := config.APIAccessRule{}
		ff.GenerateStruct(&rule)
		if rule.Version == "" {
			return
		}
		if rule.Version != endpoint.Version {
			return
		}
		if rule.Name == "" {
			return
		}

		if len(endpoint.Route) < 2 {
			return
		}

		if len(rule.Name) < 2 {
			return
		}

		if endpoint.Route[0] == rule.Name[0] {
			return
		}

		if endpoint.Route[1] == rule.Name[1] {
			return
		}

		if endpointMatchesAPIAccessRule(endpoint, rule) == true {
			panic("Should not be true")
		}
	})
}

func FuzzHTTPRegex(f *testing.F) {
	f.Fuzz(func(t *testing.T, path string) {
		parameterFinder, _ := regexp.Compile("/{.*}")
		_ = parameterFinder.MatchString(path)
	})
}
