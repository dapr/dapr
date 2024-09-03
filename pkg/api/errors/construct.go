/*
Copyright 2024 The Dapr Authors
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

package errors

type ReasonSegment string

// ConstructReason is used to append several Error Reason const strings into 1 Error Reason.
// The format of the string should generally follow this pattern:
// apierrors.CodePrefix<Dapr_API>_, apierrors.InFix<const>_,apierrors.PostFix<const>
func ConstructReason(vars ...ReasonSegment) string {
	result := ""

	for _, segment := range vars {
		result += string(segment)
	}
	return result
}
