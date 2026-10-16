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

package method

import "testing"

// TestCleanPath verifies trailing-slash preservation and, in particular, that a
// path resolving to a dot-segment ("." or "..") does NOT get a slash re-appended.
func TestCleanPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no trailing slash", "foo/bar", "foo/bar"},
		{"trailing slash preserved", "foo/bar/", "foo/bar/"},
		{"root stays root", "/", "/"},
		{"dot segment not re-slashed", "./", "."},
		{"dotdot segment not re-slashed", "../", ".."},
		{"resolves to dot", "foo/../", "."},
		{"nested traversal keeps trailing slash", "../../", "../../"},
		{"resolves to dir with trailing slash", "foo/bar/../", "foo/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CleanPath(tc.in); got != tc.want {
				t.Errorf("CleanPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
