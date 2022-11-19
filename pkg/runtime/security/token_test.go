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

package security

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/runtime/security/consts"
)

func TestAPIToken(t *testing.T) {
	t.Run("existing token", func(t *testing.T) {
		/* #nosec */
		token := "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJPbmxpbmUgSldUIEJ1aWxkZXIiLCJpYXQiOjE1OTA1NTQ1NzMsImV4cCI6MTYyMjA5MDU3MywiYXVkIjoid3d3LmV4YW1wbGUuY29tIiwic3ViIjoianJvY2tldEBleGFtcGxlLmNvbSIsIkdpdmVuTmFtZSI6IkpvaG5ueSIsIlN1cm5hbWUiOiJSb2NrZXQiLCJFbWFpbCI6Impyb2NrZXRAZXhhbXBsZS5jb20iLCJSb2xlIjpbIk1hbmFnZXIiLCJQcm9qZWN0IEFkbWluaXN0cmF0b3IiXX0.QLFl8ZqC48DOsT7SmXA794nivmqGgylzjrUu6JhXPW4"
		t.Setenv(consts.APITokenEnvVar, token)

		apitoken := GetAPIToken()
		assert.Equal(t, token, apitoken)
	})

	t.Run("non-existent token", func(t *testing.T) {
		token := GetAPIToken()
		assert.Equal(t, "", token)
	})
}

func TestAppToken(t *testing.T) {
	t.Run("existing token", func(t *testing.T) {
		/* #nosec */
		token := "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJPbmxpbmUgSldUIEJ1aWxkZXIiLCJpYXQiOjE1OTA1NTQ1NzMsImV4cCI6MTYyMjA5MDU3MywiYXVkIjoid3d3LmV4YW1wbGUuY29tIiwic3ViIjoianJvY2tldEBleGFtcGxlLmNvbSIsIkdpdmVuTmFtZSI6IkpvaG5ueSIsIlN1cm5hbWUiOiJSb2NrZXQiLCJFbWFpbCI6Impyb2NrZXRAZXhhbXBsZS5jb20iLCJSb2xlIjpbIk1hbmFnZXIiLCJQcm9qZWN0IEFkbWluaXN0cmF0b3IiXX0.QLFl8ZqC48DOsT7SmXA794nivmqGgylzjrUu6JhXPW4"
		t.Setenv(consts.AppAPITokenEnvVar, token)

		apitoken := GetAppToken()
		assert.Equal(t, token, apitoken)
	})

	t.Run("non-existent token", func(t *testing.T) {
		token := GetAppToken()
		assert.Equal(t, "", token)
	})
}

func TestExcludedRoute(t *testing.T) {
	t.Run("healthz route is excluded", func(t *testing.T) {
		route := "v1.0/healthz"
		excluded := ExcludedRoute(route)
		assert.True(t, excluded)
	})

	t.Run("custom route is not excluded", func(t *testing.T) {
		route := "v1.0/state"
		excluded := ExcludedRoute(route)
		assert.False(t, excluded)
	})
}
