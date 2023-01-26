package kubernetes

import (
	"context"
	"fmt"
	"strings"

	kauthapi "k8s.io/api/authentication/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	kauth "k8s.io/client-go/kubernetes/typed/authentication/v1"

	sentryConsts "github.com/dapr/dapr/pkg/sentry/consts"
	"github.com/dapr/dapr/pkg/sentry/identity"
	"github.com/dapr/kit/logger"
)

const errPrefix = "csr validation failed"

var log = logger.NewLogger("dapr.sentry.identity.kubernetes")

func NewValidator(client k8s.Interface, audiences []string, noDefaultTokenAudience bool) identity.Validator {
	return &validator{
		client:    client,
		auth:      client.AuthenticationV1(),
		audiences: audiences,
		// TODO: Remove once the NoDefaultTokenAudience feature is finalized
		noDefaultTokenAudience: noDefaultTokenAudience,
	}
}

type validator struct {
	client    k8s.Interface
	auth      kauth.AuthenticationV1Interface
	audiences []string
	// TODO: Remove once the NoDefaultTokenAudience feature is finalized
	noDefaultTokenAudience bool
}

func (v *validator) Validate(ctx context.Context, id, token, namespace string) error {
	if id == "" {
		return fmt.Errorf("%s: id field in request must not be empty", errPrefix)
	}
	if token == "" {
		return fmt.Errorf("%s: token field in request must not be empty", errPrefix)
	}

	// TODO: Remove once the NoDefaultTokenAudience feature is finalized
	var canTryWithNilAudience, showDefaultTokenAudienceWarning bool

	audiences := v.audiences
	if len(audiences) == 0 {
		audiences = []string{sentryConsts.ServiceAccountTokenAudience}

		// TODO: Remove once the NoDefaultTokenAudience feature is finalized
		// Because the user did not specify an explicit audience and is instead relying on the default, if the authentication fails we can retry with nil audience
		canTryWithNilAudience = !v.noDefaultTokenAudience
	}
	tokenReview := &kauthapi.TokenReview{
		Spec: kauthapi.TokenReviewSpec{
			Token:     token,
			Audiences: audiences,
		},
	}

tr: // TODO: Remove once the NoDefaultTokenAudience feature is finalized

	prts, err := v.executeTokenReview(ctx, tokenReview)
	if err != nil {
		// TODO: Remove once the NoDefaultTokenAudience feature is finalized
		if canTryWithNilAudience {
			// Retry with a nil audience, which means the default audience for the K8s API server
			tokenReview.Spec.Audiences = nil
			showDefaultTokenAudienceWarning = true
			canTryWithNilAudience = false
			goto tr
		}

		return err
	}

	// TODO: Remove once the NoDefaultTokenAudience feature is finalized
	if showDefaultTokenAudienceWarning {
		log.Warn("WARNING: Sentry accepted a token with the audience for the Kubernetes API server. This is deprecated and only supported to ensure a smooth upgrade from Dapr pre-1.10.")
	}

	if len(prts) != 4 || prts[0] != "system" {
		return fmt.Errorf("%s: provided token is not a properly structured service account token", errPrefix)
	}

	podSa := prts[3]
	podNs := prts[2]

	if namespace != "" {
		if podNs != namespace {
			return fmt.Errorf("%s: namespace mismatch. received namespace: %s", errPrefix, namespace)
		}
	}

	if id != podNs+":"+podSa {
		return fmt.Errorf("%s: token/id mismatch. received id: %s", errPrefix, id)
	}
	return nil
}

// Executes a tokenReview, returning an error if the token is invalid or if there's a failure
func (v *validator) executeTokenReview(ctx context.Context, tokenReview *kauthapi.TokenReview) ([]string, error) {
	review, err := v.auth.TokenReviews().Create(ctx, tokenReview, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("%s: token review failed: %w", errPrefix, err)
	}
	if review.Status.Error != "" {
		return nil, fmt.Errorf("%s: invalid token: %s", errPrefix, review.Status.Error)
	}
	if !review.Status.Authenticated {
		return nil, fmt.Errorf("%s: authentication failed", errPrefix)
	}
	return strings.Split(review.Status.User.Username, ":"), nil
}
