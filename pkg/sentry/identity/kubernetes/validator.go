package kubernetes

import (
	"fmt"
	"strings"

	"github.com/dapr/dapr/pkg/sentry/identity"
	kauthapi "k8s.io/api/authentication/v1"
	k8s "k8s.io/client-go/kubernetes"
	kauth "k8s.io/client-go/kubernetes/typed/authentication/v1"
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

func (v *validator) Validate(id, token string) error {
	if id == "" {
		return fmt.Errorf("%s: id field in request must not be empty", errPrefix)
	}
	if token == "" {
		return fmt.Errorf("%s: token field in request must not be empty", errPrefix)
	}

	review, err := v.auth.TokenReviews().Create(&kauthapi.TokenReview{Spec: kauthapi.TokenReviewSpec{Token: token}})
	if err != nil {
		return err
	}

	if review.Status.Error != "" {
		return fmt.Errorf("%s: invalid token: %s", errPrefix, review.Status.Error)
	}
	if !review.Status.Authenticated {
		return fmt.Errorf("%s: authentication failed", errPrefix)
	}

	prts := strings.Split(review.Status.User.Username, ":")
	if len(prts) != 4 || prts[0] != "system" {
		return fmt.Errorf("%s: provided token is not a properly structured service account token", errPrefix)
	}

	podSa := prts[3]
	podNs := prts[2]
	if id != fmt.Sprintf("%s:%s", podSa, podNs) {
		return fmt.Errorf("%s: token/id mismatch. received id: %s", errPrefix, id)
	}
	return nil
}
