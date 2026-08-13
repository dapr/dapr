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

package components

import (
	"strings"

	"github.com/dapr/kit/logger"
)

// VersionConstructor is a version name func pair used to construct a
// component.
type VersionConstructor struct {
	Version     string
	Constructor any
}

// Versioning is a struct that contains the versioning information for a single
// component Type. It is expected that each VersionConstructor be unique.
type Versioning struct {
	// Preferred is the preferred version to use, used to log a warning if a
	// deprecated version is used.
	Preferred VersionConstructor

	// Deprecated is a list of deprecated versions to log a warning if used.
	Deprecated []VersionConstructor

	// Others is a list of other versions that are supported, but not preferred.
	Others []VersionConstructor

	// Default is the default version to use when no version is specified. This
	// should make a VersionConstructor from the set above.
	Default string
}

// IsInitialVersion returns true when a version is considered an unstable version (v0)
// or first stable version (v1). For backward compatibility, empty strings are also included.
func IsInitialVersion(version string) bool {
	v := strings.ToLower(version)
	return v == "" || v == UnstableVersion || v == FirstStableVersion
}

// CheckDeprecated checks if a version is deprecated and logs a warning if it
// is using information derived from the version set.
func CheckDeprecated(l logger.Logger, name, version string, versionSet Versioning) {
	log := logger.FromLogger(l)
	for _, v := range versionSet.Deprecated {
		if v.Version == version {
			log.Warn(
				"state store version is deprecated and will be removed in a future version",
				"name", name, "version", version, "preferred_version", versionSet.Preferred.Version)
		}
	}
}

const (
	// Unstable version (v0).
	UnstableVersion = "v0"

	// First stable version (v1).
	FirstStableVersion = "v1"
)
