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

package processor

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
)

func Test_scopeFilterSubscriptions(t *testing.T) {
	tests := map[string]struct {
		input []subapi.Subscription
		exp   []subapi.Subscription
	}{
		"nil subs": {
			input: nil,
			exp:   []subapi.Subscription{},
		},
		"no subs": {
			input: []subapi.Subscription{},
			exp:   []subapi.Subscription{},
		},
		"single no scope": {
			input: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
					Scopes:     []string{},
				},
			},
			exp: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
					Scopes:     []string{},
				},
			},
		},
		"multiple no scope": {
			input: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
					Scopes:     []string{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub2"},
					Scopes:     []string{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub3"},
					Scopes:     []string{},
				},
			},
			exp: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
					Scopes:     []string{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub2"},
					Scopes:     []string{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub3"},
					Scopes:     []string{},
				},
			},
		},
		"single scoped": {
			input: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
					Scopes:     []string{"id-1", "id-2"},
				},
			},
			exp: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
					Scopes:     []string{"id-1", "id-2"},
				},
			},
		},
		"multiple scoped": {
			input: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
					Scopes:     []string{"id-1", "id-2"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub2"},
					Scopes:     []string{"id-1"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub3"},
					Scopes:     []string{},
				},
			},
			exp: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
					Scopes:     []string{"id-1", "id-2"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub2"},
					Scopes:     []string{"id-1"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub3"},
					Scopes:     []string{},
				},
			},
		},
		"single out of scope": {
			input: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
					Scopes:     []string{"id-2"},
				},
			},
			exp: []subapi.Subscription{},
		},
		"multiple out of scope": {
			input: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
					Scopes:     []string{"id-3", "id-2"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub2"},
					Scopes:     []string{"id-2"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub3"},
					Scopes:     []string{"id-3"},
				},
			},
			exp: []subapi.Subscription{},
		},
		"multiple some scoped": {
			input: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
					Scopes:     []string{"id-3", "id-2"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub2"},
					Scopes:     []string{"id-1"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub3"},
					Scopes:     []string{"id-3", "id-1"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub4"},
					Scopes:     []string{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub5"},
					Scopes:     []string{"id-3", "id-4"},
				},
			},
			exp: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub2"},
					Scopes:     []string{"id-1"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub3"},
					Scopes:     []string{"id-3", "id-1"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub4"},
					Scopes:     []string{},
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := (&Processor{appID: "id-1"}).scopeFilterSubscriptions(test.input)
			require.Equal(t, test.exp, got)
		})
	}
}
