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

var wasmComponentsMap = map[string]struct{}{}

func RegisterWasmComponentType(category Category, typeName string) {
	wasmComponentsMap[string(category)+"."+typeName] = struct{}{}
}

func IsWasmComponentType(componentType string) bool {
	_, ok := wasmComponentsMap[componentType]
	return ok
}
