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

// Versioning is a struct that contains the versioning information for a single
// component Type.
type Versioning struct {
	Default    string
	Preferred  string
	Deprecated []string
}

// IsInitialVersion returns true when a version is considered an unstable version (v0)
// or first stable version (v1). For backward compatibility, empty strings are also included.
func IsInitialVersion(version string) bool {
	v := strings.ToLower(version)
	return v == "" || v == UnstableVersion || v == FirstStableVersion
}

func CheckDeprecated(log logger.Logger, name, version string, verSet map[string]Versioning) {
	if dep, ok := verSet[name]; ok {
		for _, v := range dep.Deprecated {
			if v == version {
				log.Warnf(
					"WARNING: state store %[1]s/%[2]s is deprecated and will be removed in a future version, please use %[3]s/%[4]s",
					name, version, name, dep.Preferred)
			}
		}
	}
}

const (
	// Unstable version (v0).
	UnstableVersion = "v0"

	// First stable version (v1).
	FirstStableVersion = "v1"
)
