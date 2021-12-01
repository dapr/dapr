/*
Copyright 2021 The Dapr Authors
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

package version

// Values for these are injected by the build.
var (
	version = "edge"

	gitcommit, gitversion string
)

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
