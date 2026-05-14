/*
Copyright 2026 The Dapr Authors
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

package mcp

import "google.golang.org/protobuf/types/known/structpb"

// argsAsStruct converts a Go map of tool arguments into google.protobuf.Struct
// for use inside hook input messages (which carry arguments as Struct so any
// language can deserialize them generically).
func argsAsStruct(args map[string]any) *structpb.Struct {
	if args == nil {
		return nil
	}
	s, err := structpb.NewStruct(args)
	if err != nil {
		return nil
	}
	return s
}

// structAsArgs converts a google.protobuf.Struct back into a Go map.
func structAsArgs(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}
	return s.AsMap()
}
