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

package buildinfo

import (
	"strings"
)

// Values for these are injected by the build.
var (
	version = "edge"

	gitcommit, gitversion string
	features              string
)

// Set by the init function
var featuresSlice []string

func init() {
	if featuresSlice != nil {
		// Return if another init method (e.g. for a build tag) already initialized
		return
	}

	// At initialization, parse the value of "features" into a slice
	if features == "" {
		featuresSlice = []string{}
	} else {
		featuresSlice = strings.Split(features, ",")
	}
	features = ""
}

// Version returns the Dapr version. This is either a semantic version
// number or else, in the case of unreleased code, the string "edge".
func Version() string {
	return version
}

// Commit returns the git commit SHA for the code that Dapr was built from.
func Commit() string {
	return gitcommit
}

// GitVersion returns the git version for the code that Dapr was built from.
func GitVersion() string {
	return gitversion
}

// Features returns the list of features enabled for this build.
func Features() []string {
	return featuresSlice
}

// AddFeature adds a new feature to the featuresSlice. It's primarily meant for testing purposes.
// This should only be called as part of an init() method.
func AddFeature(feature string) {
	if featuresSlice == nil {
		// If featuresSlice is nil, it means the caller's init() was executed before this package's
		features += "," + feature
	} else {
		featuresSlice = append(featuresSlice, feature)
	}
}
