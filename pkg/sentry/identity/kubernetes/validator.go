package kubernetes

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	kauthapi "k8s.io/api/authentication/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	kauth "k8s.io/client-go/kubernetes/typed/authentication/v1"

	"github.com/dapr/dapr/pkg/sentry/identity"
)

const (
	errPrefix = "csr validation failed"
)

func NewValidator(client k8s.Interface) identity.Validator {
	return &validator{
		client: client,
		auth:   client.AuthenticationV1(),
	}
}

type validator struct {
	client k8s.Interface
	auth   kauth.AuthenticationV1Interface
}

func (v *validator) Validate(id, token, namespace string) error {
	if id == "" {
		return errors.Errorf("%s: id field in request must not be empty", errPrefix)
	}
	if token == "" {
		return errors.Errorf("%s: token field in request must not be empty", errPrefix)
	}

	review, err := v.auth.TokenReviews().Create(context.TODO(), &kauthapi.TokenReview{Spec: kauthapi.TokenReviewSpec{Token: token}}, v1.CreateOptions{})
	if err != nil {
		return err
	}

	if review.Status.Error != "" {
		return errors.Errorf("%s: invalid token: %s", errPrefix, review.Status.Error)
	}
	if !review.Status.Authenticated {
		return errors.Errorf("%s: authentication failed", errPrefix)
	}

	prts := strings.Split(review.Status.User.Username, ":")
	if len(prts) != 4 || prts[0] != "system" {
		return errors.Errorf("%s: provided token is not a properly structured service account token", errPrefix)
	}

	podSa := prts[3]
	podNs := prts[2]

	if namespace != "" {
		if podNs != namespace {
			return errors.Errorf("%s: namespace mismatch. received namespace: %s", errPrefix, namespace)
		}
	}

	if id != fmt.Sprintf("%s:%s", podNs, podSa) {
		return errors.Errorf("%s: token/id mismatch. received id: %s", errPrefix, id)
	}
	return nil
}
