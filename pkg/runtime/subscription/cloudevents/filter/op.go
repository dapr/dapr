/*
Copyright 2025 The Dapr Authors
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

package filter

import "strings"

type expressionOp func(any, string) bool

func makeOp(path []string, value string, op expressionOp) FilterFunc {
	return func(m map[string]any) bool {
		var val any = m
		for _, segment := range path {
			mp, ok := val.(map[string]any)
			if !ok {
				return false
			}
			val, ok = mp[segment]
			if !ok {
				return false
			}
		}

		str, ok := val.(string)
		return ok && op(str, value)
	}
}

func opExact(a any, b string) bool {
	return a == b
}

func opSuffix(a any, b string) bool {
	str, ok := a.(string)
	return ok && strings.HasSuffix(str, b)
}

func opPrefix(a any, b string) bool {
	str, ok := a.(string)
	return ok && strings.HasPrefix(str, b)
}

type filterOp func(a, b FilterFunc) FilterFunc

func makeFilter(a, b FilterFunc, op filterOp) FilterFunc {
	return func(m map[string]any) bool {
		return op(a, b)(m)
	}
}

func filterAnd(a, b FilterFunc) FilterFunc {
	return func(m map[string]any) bool {
		return a(m) && b(m)
	}
}

func filterOr(a, b FilterFunc) FilterFunc {
	return func(m map[string]any) bool {
		return a(m) || b(m)
	}
}

func filterNot(a FilterFunc) FilterFunc {
	return func(m map[string]any) bool {
		return !a(m)
	}
}
