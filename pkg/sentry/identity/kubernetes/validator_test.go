package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	fake "k8s.io/client-go/kubernetes/fake"
)

func TestValidate(t *testing.T) {
	t.Run("invalid token", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset()
		v := validator{
			client: fakeClient,
			auth:   fakeClient.AuthenticationV1(),
		}
	
		err := v.Validate("a1:ns1", "a2:ns2")
		assert.NotNil(t, err)
	})

	t.Run("empty token", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset()
		v := validator{
			client: fakeClient,
			auth:   fakeClient.AuthenticationV1(),
		}
	
		err := v.Validate("a1:ns1", "")
		assert.NotNil(t, err)
	})

	t.Run("empty id", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset()
		v := validator{
			client: fakeClient,
			auth:   fakeClient.AuthenticationV1(),
		}
	
		err := v.Validate("", "a1:ns1")
		assert.NotNil(t, err)
	})
}
