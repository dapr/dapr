package security

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAPIToken(t *testing.T) {
	t.Run("init with token from env", func(t *testing.T) {
		token := "123456"
		t.Setenv(APITokenEnvVar, token)

		apitoken := &APIToken{}
		apitoken.Init()
		assert.Equal(t, token, string(apitoken.token))
		assert.True(t, apitoken.HasAPIToken())
	})

	t.Run("init with token from string", func(t *testing.T) {
		token := "123456"
		apitoken := &APIToken{}
		apitoken.InitWithToken(token)
		assert.Equal(t, token, string(apitoken.token))
		assert.True(t, apitoken.HasAPIToken())
	})

	t.Run("init with non-existent token", func(t *testing.T) {
		apitoken := &APIToken{}
		apitoken.Init()
		assert.Equal(t, "", string(apitoken.token))
		assert.False(t, apitoken.HasAPIToken())
	})

	t.Run("validating tokens", func(t *testing.T) {
		token := "123456"
		apitoken := &APIToken{}
		apitoken.InitWithToken(token)
		assert.True(t, apitoken.CheckAPIToken([]byte(token)))
		assert.False(t, apitoken.CheckAPIToken([]byte("invalid")))
	})

	t.Run("delays after failed auth", func(t *testing.T) {
		token := "123456"
		delay := 500
		apitoken := &APIToken{
			DelayOnFailed: time.Duration(delay) * time.Millisecond,
		}
		apitoken.InitWithToken(token)

		tests := []struct {
			token       string
			shouldOk    bool
			shouldDelay bool
		}{
			{token: token, shouldOk: true, shouldDelay: false},
			{token: token, shouldOk: true, shouldDelay: false},
			{token: "foo", shouldOk: false, shouldDelay: false},
			{token: "foo", shouldOk: false, shouldDelay: true},
			{token: token, shouldOk: true, shouldDelay: true},
			{token: token, shouldOk: true, shouldDelay: false},
			{token: token, shouldOk: true, shouldDelay: false},
			{token: "foo", shouldOk: false, shouldDelay: false},
			{token: "foo", shouldOk: false, shouldDelay: true},
			{token: token, shouldOk: true, shouldDelay: true},
			{token: token, shouldOk: true, shouldDelay: false},
		}
		for _, test := range tests {
			start := time.Now()
			res := apitoken.CheckAPIToken([]byte(test.token))
			elapsed := time.Since(start).Milliseconds()
			assert.Equal(t, test.shouldOk, res)
			if test.shouldDelay {
				assert.GreaterOrEqual(t, elapsed, int64(delay))
			} else {
				assert.Less(t, elapsed, int64(delay-100))
			}
		}
	})
}

func TestAppToken(t *testing.T) {
	t.Run("existing token", func(t *testing.T) {
		/* #nosec */
		token := "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJPbmxpbmUgSldUIEJ1aWxkZXIiLCJpYXQiOjE1OTA1NTQ1NzMsImV4cCI6MTYyMjA5MDU3MywiYXVkIjoid3d3LmV4YW1wbGUuY29tIiwic3ViIjoianJvY2tldEBleGFtcGxlLmNvbSIsIkdpdmVuTmFtZSI6IkpvaG5ueSIsIlN1cm5hbWUiOiJSb2NrZXQiLCJFbWFpbCI6Impyb2NrZXRAZXhhbXBsZS5jb20iLCJSb2xlIjpbIk1hbmFnZXIiLCJQcm9qZWN0IEFkbWluaXN0cmF0b3IiXX0.QLFl8ZqC48DOsT7SmXA794nivmqGgylzjrUu6JhXPW4"
		t.Setenv(AppAPITokenEnvVar, token)

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
