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

package secret

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/kit/logger"
)

func TestIsSensitiveMetadataName(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  bool
	}{
		{name: "redis password", field: "redisPassword", want: true},
		{name: "generic password", field: "password", want: true},
		{name: "connection string", field: "connectionString", want: true},
		{name: "account key", field: "accountKey", want: true},
		{name: "api key underscore", field: "api_key", want: true},
		{name: "api key hyphen", field: "api-key", want: true},
		{name: "mixed case", field: "RedisPassword", want: true},
		{name: "access token", field: "accessToken", want: true},
		{name: "client secret", field: "clientSecret", want: true},
		{name: "redis host", field: "redisHost", want: false},
		{name: "key prefix", field: "keyPrefix", want: false},
		{name: "secrets file", field: "secretsFile", want: false},
		{name: "empty name", field: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isSensitiveMetadataName(tt.field))
		})
	}
}

func TestWarnPlaintextCredentials(t *testing.T) {
	plaintextItem := func(name, value string) commonapi.NameValuePair {
		return commonapi.NameValuePair{
			Name: name,
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{Raw: []byte(value)},
			},
		}
	}

	newComp := func(items ...commonapi.NameValuePair) componentsapi.Component {
		return componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "statestore"},
			Spec: componentsapi.ComponentSpec{
				Type:     "state.redis",
				Version:  "v1",
				Metadata: items,
			},
		}
	}

	captureLog := func(t *testing.T) *bytes.Buffer {
		t.Helper()
		buf := new(bytes.Buffer)
		l := logger.NewLogger("dapr.runtime.processor.secret")
		l.SetOutput(buf)
		t.Cleanup(func() { l.SetOutput(os.Stdout) })
		return buf
	}

	t.Run("plaintext password", func(t *testing.T) {
		buf := captureLog(t)
		comp := newComp(plaintextItem("redisPassword", "MyPassword"))
		WarnPlaintextCredentials(&comp)
		assert.Contains(t, buf.String(), "redisPassword")
		assert.Contains(t, buf.String(), "secretKeyRef")
	})

	t.Run("plaintext connection string", func(t *testing.T) {
		buf := captureLog(t)
		comp := newComp(plaintextItem("connectionString", "Endpoint=sb://example"))
		WarnPlaintextCredentials(&comp)
		assert.Contains(t, buf.String(), "connectionString")
		assert.Contains(t, buf.String(), "secretKeyRef")
	})

	t.Run("uses secretKeyRef", func(t *testing.T) {
		buf := captureLog(t)
		item := plaintextItem("redisPassword", "MyPassword")
		item.SecretKeyRef = commonapi.SecretKeyRef{Name: "redis-secret", Key: "redis-password"}
		comp := newComp(item)
		WarnPlaintextCredentials(&comp)
		assert.Empty(t, buf.String())
	})

	t.Run("uses envRef", func(t *testing.T) {
		buf := captureLog(t)
		item := plaintextItem("redisPassword", "MyPassword")
		item.EnvRef = "REDIS_PASSWORD"
		comp := newComp(item)
		WarnPlaintextCredentials(&comp)
		assert.Empty(t, buf.String())
	})

	t.Run("empty value", func(t *testing.T) {
		buf := captureLog(t)
		comp := newComp(plaintextItem("redisPassword", ""))
		WarnPlaintextCredentials(&comp)
		assert.Empty(t, buf.String())
	})

	t.Run("non-secret field", func(t *testing.T) {
		buf := captureLog(t)
		comp := newComp(plaintextItem("redisHost", "localhost:6379"))
		WarnPlaintextCredentials(&comp)
		assert.Empty(t, buf.String())
	})

	t.Run("mixed metadata", func(t *testing.T) {
		buf := captureLog(t)
		comp := newComp(
			plaintextItem("redisHost", "localhost:6379"),
			plaintextItem("redisPassword", "MyPassword"),
		)
		WarnPlaintextCredentials(&comp)
		assert.Contains(t, buf.String(), "redisPassword")
		assert.NotContains(t, buf.String(), "redisHost")
	})

	t.Run("log includes identity", func(t *testing.T) {
		buf := captureLog(t)
		comp := newComp(plaintextItem("redisPassword", "MyPassword"))
		WarnPlaintextCredentials(&comp)
		assert.Contains(t, buf.String(), "Component")
		assert.Contains(t, buf.String(), "statestore")
	})
}
