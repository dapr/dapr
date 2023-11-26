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

package authorizer

import (
	"reflect"
	"strings"
	"testing"

	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

func TestComponentDenyList(t *testing.T) {
	type args struct {
		raw []string
	}
	tests := []struct {
		name    string
		args    args
		want    componentDenyList
		allowed map[string]bool
	}{
		{
			name: "empty",
			args: args{[]string{}},
			want: componentDenyList{
				list: []componentDenyListItem{},
			},
			allowed: map[string]bool{
				"":                   false,
				"no.version":         false,
				"state.foo/v1":       true,
				"state.foo/v2":       true,
				"state.bar/v1alpha1": true,
			},
		},
		{
			name: "one item, no version",
			args: args{[]string{"state.foo"}},
			want: componentDenyList{
				list: []componentDenyListItem{
					{typ: "state.foo", version: ""},
				},
			},
			allowed: map[string]bool{
				"":                   false,
				"no.version":         false,
				"state.foo/v1":       false,
				"state.foo/v2":       false,
				"state.bar/v1alpha1": true,
			},
		},
		{
			name: "one item and version",
			args: args{[]string{"state.foo/v2"}},
			want: componentDenyList{
				list: []componentDenyListItem{
					{typ: "state.foo", version: "v2"},
				},
			},
			allowed: map[string]bool{
				"state.foo/v1":       true,
				"state.foo/v2":       false,
				"state.bar/v1alpha1": true,
			},
		},
		{
			name: "one item with version, one without",
			args: args{[]string{"state.foo", "state.bar/v2"}},
			want: componentDenyList{
				list: []componentDenyListItem{
					{typ: "state.foo", version: ""},
					{typ: "state.bar", version: "v2"},
				},
			},
			allowed: map[string]bool{
				"state.foo/v1":       false,
				"state.foo/v2":       false,
				"state.bar/v1alpha1": true,
			},
		},
		{
			name: "invalid items",
			args: args{[]string{"state.foo", "state.bar/v2", "foo/bar/v2", ""}},
			want: componentDenyList{
				list: []componentDenyListItem{
					{typ: "state.foo", version: ""},
					{typ: "state.bar", version: "v2"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dl := newComponentDenyList(tt.args.raw)
			if !reflect.DeepEqual(dl, tt.want) {
				t.Errorf("newComponentDenyList() = %v, want %v", dl, tt.want)
			}

			if len(tt.allowed) == 0 {
				return
			}

			for compStr, wantAllowed := range tt.allowed {
				parts := strings.Split(compStr, "/")
				typ := parts[0]
				var ver string
				if len(parts) > 1 {
					ver = parts[1]
				}
				comp := componentsV1alpha1.Component{
					Spec: componentsV1alpha1.ComponentSpec{
						Type:    typ,
						Version: ver,
					},
				}

				if gotAllowed := dl.IsAllowed(comp); gotAllowed != wantAllowed {
					t.Errorf("IsAllowed(%v) = %v, want %v", compStr, gotAllowed, wantAllowed)
				}
			}
		})
	}
}
