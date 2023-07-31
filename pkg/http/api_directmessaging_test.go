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

package http

import (
	"net/http"
	"testing"
)

func TestPathHasPrefix(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		prefixParts  []string
		want         int
		wantTrailing string
	}{
		{name: "match one prefix", path: "/v1.0/invoke/foo", prefixParts: []string{"v1.0"}, want: 6, wantTrailing: "invoke/foo"},
		{name: "match two prefixes", path: "/v1.0/invoke/foo", prefixParts: []string{"v1.0", "invoke"}, want: 13, wantTrailing: "foo"},
		{name: "ignore extra slashes", path: "//v1.0///invoke//foo", prefixParts: []string{"v1.0", "invoke"}, want: 17, wantTrailing: "foo"},
		{name: "extra slashes after match", path: "//v1.0///invoke//foo//bar", prefixParts: []string{"v1.0", "invoke"}, want: 17, wantTrailing: "foo//bar"},
		{name: "no slash at beginning", path: "v1.0//invoke//foo", prefixParts: []string{"v1.0", "invoke"}, want: 14, wantTrailing: "foo"},
		{name: "empty prefix", path: "/foo/bar", prefixParts: []string{}, want: 1, wantTrailing: "foo/bar"},
		{name: "empty path", path: "", prefixParts: []string{"v1.0", "invoke"}, want: -1},
		{name: "no match", path: "/foo/bar", prefixParts: []string{"v1.0", "invoke"}, want: -1},
		{name: "path is slash only", path: "/", prefixParts: []string{"v1.0", "invoke"}, want: -1},
		{name: "empty prefix skips multiple slashes", path: "///", prefixParts: []string{}, want: 3, wantTrailing: ""},
		{name: "missing initial part", path: "/foo/bar", prefixParts: []string{"bar"}, want: -1},
		{name: "match incomplete", path: "/v1.0", prefixParts: []string{"v1.0", "invoke"}, want: -1},
		{name: "trailing slash is required", path: "v1.0//invoke", prefixParts: []string{"v1.0", "invoke"}, want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathHasPrefix(tt.path, tt.prefixParts...)
			if got != tt.want {
				t.Errorf("pathHasPrefix() = %v, want %v", got, tt.want)
			} else if got >= 0 && tt.path[got:] != tt.wantTrailing {
				t.Errorf("trailing = %q, want %q", tt.path[got:], tt.wantTrailing)
			}
		})
	}
}

func TestFindTargetIDAndMethod(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		headers      http.Header
		wantTargetID string
		wantMethod   string
	}{
		{name: "dapr-app-id header", path: "/foo/bar", headers: http.Header{"Dapr-App-Id": []string{"myapp"}}, wantTargetID: "myapp", wantMethod: "foo/bar"},
		{name: "basic auth", path: "/foo/bar", headers: http.Header{"Authorization": []string{"Basic ZGFwci1hcHAtaWQ6YXV0aA=="}}, wantTargetID: "auth", wantMethod: "foo/bar"},
		{name: "dapr-app-id header has priority over basic auth", path: "/foo/bar", headers: http.Header{"Dapr-App-Id": []string{"myapp"}, "Authorization": []string{"Basic ZGFwci1hcHAtaWQ6YXV0aA=="}}, wantTargetID: "myapp", wantMethod: "foo/bar"},
		{name: "path with internal target", path: "/v1.0/invoke/myapp/method/foo", wantTargetID: "myapp", wantMethod: "foo"},
		{name: "basic auth has priority over path", path: "/v1.0/invoke/myapp/method/foo", headers: http.Header{"Authorization": []string{"Basic ZGFwci1hcHAtaWQ6YXV0aA=="}}, wantTargetID: "auth", wantMethod: "v1.0/invoke/myapp/method/foo"},
		{name: "path with '/' method", path: "/v1.0/invoke/myapp/method/", wantTargetID: "myapp", wantMethod: ""},
		{name: "path with missing method", path: "/v1.0/invoke/myapp/method", wantTargetID: "", wantMethod: ""},
		{name: "path with http target unescaped", path: "/v1.0/invoke/http://example.com/method/foo", wantTargetID: "http://example.com", wantMethod: "foo"},
		{name: "path with https target unescaped", path: "/v1.0/invoke/https://example.com/method/foo", wantTargetID: "https://example.com", wantMethod: "foo"},
		{name: "path with http target escaped", path: "/v1.0/invoke/http%3A%2F%2Fexample.com/method/foo", wantTargetID: "http://example.com", wantMethod: "foo"},
		{name: "path with https target escaped", path: "/v1.0/invoke/https%3A%2F%2Fexample.com/method/foo", wantTargetID: "https://example.com", wantMethod: "foo"},
		{name: "path with https target partly escaped", path: "/v1.0/invoke/https%3A/%2Fexample.com/method/foo", wantTargetID: "https://example.com", wantMethod: "foo"},
		{name: "extra slashes are removed", path: "///foo//bar", headers: http.Header{"Dapr-App-Id": []string{"myapp"}}, wantTargetID: "myapp", wantMethod: "foo/bar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTargetID, gotMethod := findTargetIDAndMethod(tt.path, tt.headers)
			if gotTargetID != tt.wantTargetID {
				t.Errorf("findTargetIDAndMethod() gotTargetID = %v, want %v", gotTargetID, tt.wantTargetID)
			}
			if gotMethod != tt.wantMethod {
				t.Errorf("findTargetIDAndMethod() gotMethod = %v, want %v", gotMethod, tt.wantMethod)
			}
		})
	}
}
