//go:build !allcomponents && !stablecomponents

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

package components

// This file is built when neither "allcomponents" nor "stablecomponents" are set as Go build tags.
// Its purpose is to provide a more user-friendly error to developers.
func init() {
	panic("When building github.com/dapr/dapr/cmd/daprd, you must use either '-tags stablecomponents' or `-tags allcomponents'")
}
