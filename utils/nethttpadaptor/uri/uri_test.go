/*
Copyright 2022 The Dapr Authors
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

package uri

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_NormalizePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.SkipNow()
	}

	t.Parallel()

	tests := map[string]string{
		"/aa//bb":                     "/aa/bb",
		"/x///y/":                     "/x/y/",
		"/abc//de///fg////":           "/abc/de/fg/",
		"/xxxx%2fy//yy%2f%2/F%2F///":  "/xxxx%2fy/yy%2f%2/F%2F/",
		"/aaa/..":                     "/",
		"/xxx/yyy/../":                "/xxx/",
		"/aaa/bbb/ccc/../../ddd":      "/aaa/ddd",
		"/a/b/../c/d/../e/..":         "/a/c/",
		"/aaa/../../../../xxx":        "/xxx",
		"/../../../../../..":          "/",
		"/../../../../../../":         "/",
		"/////aaa%2Fbbb%2F%2E.%2Fxxx": "/aaa%2Fbbb%2F%2E.%2Fxxx",
		"/aaa////..//b":               "/b",
		"/aaa/..bbb/ccc/..":           "/aaa/..bbb/",
		"/a/./b/././c/./d.html":       "/a/b/c/d.html",
		"./foo/":                      "/foo/",
		"./../.././../../aaa/bbb/../../../././../": "/",
		"./a/./.././../b/./foo.html":               "/b/foo.html",
	}

	for input, expout := range tests {
		t.Run(input, func(t *testing.T) {
			var output []byte
			output = NormalizePath(output, []byte(input))

			assert.Equal(t, expout, string(output), "input: "+input)
		})
	}
}
