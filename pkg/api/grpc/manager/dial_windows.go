//go:build windows
// +build windows

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

package manager

import (
	"github.com/dapr/dapr/pkg/modes"
)

// GetDialAddressPrefix returns a dial prefix for a gRPC client connections for a given DaprMode.
// This is used on Windows hosts.
func GetDialAddressPrefix(mode modes.DaprMode) string {
	return ""
}
