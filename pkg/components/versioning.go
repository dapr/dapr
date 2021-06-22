// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package components

import "strings"

// IsInitialVersion returns true when a version is considered an unstable version (v0)
// or first stable version (v1). For backward compatibility, empty strings are also included.
func IsInitialVersion(version string) bool {
	v := strings.ToLower(version)
	return v == "" || v == UnstableVersion || v == FirstStableVersion
}

const (
	// Unstable version (v0).
	UnstableVersion = "v0"

	// First stable version (v1).
	FirstStableVersion = "v1"
)
