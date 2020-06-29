// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package version

// Values for these are injected by the build.
var (
	version = "edge"
	commit  string
)

// Version returns the Dapr version. This is either a semantic version
// number or else, in the case of unreleased code, the string "edge".
func Version() string {
	return version
}

// Commit returns the git commit SHA for the code that Dapr was built from.
func Commit() string {
	return commit
}
