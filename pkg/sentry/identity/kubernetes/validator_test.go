package kubernetes

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	kauthapi "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fake "k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
)

func TestValidate(t *testing.T) {
	t.Run("invalid token", func(t *testing.T) {
		fakeClient := &fake.Clientset{}
		fakeClient.Fake.AddReactor(
			"create",
			"tokenreviews",
			func(action core.Action) (bool, runtime.Object, error) {
				return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{Error: "bad token"}}, nil
			})

		v := validator{
			client: fakeClient,
			auth:   fakeClient.AuthenticationV1(),
		}

		err := v.Validate("a1:ns1", "a2:ns2")
		assert.Equal(t, fmt.Errorf("%s: invalid token: bad token", errPrefix), err)
	})

	t.Run("unauthenticated", func(t *testing.T) {
		fakeClient := &fake.Clientset{}
		fakeClient.Fake.AddReactor(
			"create",
			"tokenreviews",
			func(action core.Action) (bool, runtime.Object, error) {
				return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{Authenticated: false}}, nil
			})

		v := validator{
			client: fakeClient,
			auth:   fakeClient.AuthenticationV1(),
		}

		err := v.Validate("a1:ns1", "a2:ns2")
		assert.Equal(t, fmt.Errorf("%s: authentication failed", errPrefix), err)
	})

	t.Run("bad token structure", func(t *testing.T) {
		fakeClient := &fake.Clientset{}
		fakeClient.Fake.AddReactor(
			"create",
			"tokenreviews",
			func(action core.Action) (bool, runtime.Object, error) {
				return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{Authenticated: true, User: kauthapi.UserInfo{Username: "name"}}}, nil
			})

		v := validator{
			client: fakeClient,
			auth:   fakeClient.AuthenticationV1(),
		}

		err := v.Validate("a1:ns1", "a2:ns2")
		assert.Equal(t, fmt.Errorf("%s: provided token is not a properly structured service account token", errPrefix), err)
	})

	t.Run("token id mismatch", func(t *testing.T) {
		fakeClient := &fake.Clientset{}
		fakeClient.Fake.AddReactor(
			"create",
			"tokenreviews",
			func(action core.Action) (bool, runtime.Object, error) {
				return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{Authenticated: true, User: kauthapi.UserInfo{Username: "system:serviceaccount:a1:ns1"}}}, nil
			})

		v := validator{
			client: fakeClient,
			auth:   fakeClient.AuthenticationV1(),
		}

		err := v.Validate("a1:ns2", "a2:ns2")
		assert.Equal(t, fmt.Errorf("%s: token/id mismatch. received id: a1:ns2", errPrefix), err)
	})

	t.Run("empty token", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset()
		v := validator{
			client: fakeClient,
			auth:   fakeClient.AuthenticationV1(),
		}

		err := v.Validate("a1:ns1", "")
		expectedErr := fmt.Errorf("%s: token field in request must not be empty", errPrefix)
		assert.Equal(t, expectedErr, err)
	})

	t.Run("empty id", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset()
		v := validator{
			client: fakeClient,
			auth:   fakeClient.AuthenticationV1(),
		}

		err := v.Validate("", "a1:ns1")
		expectedErr := fmt.Errorf("%s: id field in request must not be empty", errPrefix)
		assert.Equal(t, expectedErr, err)
	})
}
