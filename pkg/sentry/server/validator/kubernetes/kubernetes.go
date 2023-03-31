/*
Copyright 2023 The Dapr Authors
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

package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dapr/kit/logger"
	"github.com/golang-jwt/jwt/v4"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	kauthapi "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	cl "k8s.io/client-go/kubernetes"
	clauthv1 "k8s.io/client-go/kubernetes/typed/authentication/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1alpha1 "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
	"github.com/dapr/dapr/pkg/sentry/server/validator/internal"
)

const (
	errPrefix = "csr validation failed"
	// TODO: @joshvanl: Before 1.11, dapr would use this generic audience. After
	// 1.11, clients use the sentry SPIFFE ID as the audience. Remove legacy
	// audience in v1.12
	LegacyServiceAccountAudience = "dapr.io/sentry"
)

var (
	log    = logger.NewLogger("dapr.sentry.identity.kubernetes")
	scheme = runtime.NewScheme()
)

func init() {
	if err := configv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
}

type Options struct {
	RestConfig     *rest.Config
	SentryID       spiffeid.ID
	ControlPlaneNS string

	NoDefaultTokenAudience bool
}

// kubernetes implements the validator.Interface. It validates the request by
// doing a Kubernetes token review.
type kubernetes struct {
	auth           clauthv1.AuthenticationV1Interface
	client         client.Reader
	ready          func(context.Context) bool
	sentryAudience string
	controlPlaneNS string
	controlPlaneTD spiffeid.TrustDomain

	noDefaultTokenAudience bool
}

func New(ctx context.Context, opts Options) (validator.Interface, error) {
	kubeClient, err := cl.NewForConfig(opts.RestConfig)
	if err != nil {
		return nil, err
	}

	cache, err := cache.New(opts.RestConfig, cache.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	for _, obj := range []client.Object{
		&metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"}},
		&configv1alpha1.Configuration{},
	} {
		if _, err := cache.GetInformer(ctx, obj); err != nil {
			return nil, err
		}
	}

	return &kubernetes{
		auth:           kubeClient.AuthenticationV1(),
		client:         cache,
		ready:          cache.WaitForCacheSync,
		sentryAudience: opts.SentryID.String(),
		controlPlaneNS: opts.ControlPlaneNS,
		controlPlaneTD: opts.SentryID.TrustDomain(),
		// TODO: Remove once the NoDefaultTokenAudience feature is finalized
		noDefaultTokenAudience: opts.NoDefaultTokenAudience,
	}, nil
}

func (k *kubernetes) Start(ctx context.Context) error {
	if err := k.client.(cache.Cache).Start(ctx); err != nil {
		return err
	}

	<-ctx.Done()
	return nil
}

func (k *kubernetes) Validate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (spiffeid.TrustDomain, error) {
	if !k.ready(ctx) {
		return spiffeid.TrustDomain{}, errors.New("validator not ready")
	}

	// The TrustDomain field is ignored by the Kubernetes validator.
	if _, err := internal.Validate(ctx, req); err != nil {
		return spiffeid.TrustDomain{}, err
	}

	prts, err := k.executeTokenReview(ctx, req.GetToken(), LegacyServiceAccountAudience, k.sentryAudience)
	if err != nil {
		if !k.noDefaultTokenAudience {
			// Empty audience means the Kubernetes API server.
			prts, err = k.executeTokenReview(ctx, req.GetToken())
			if err != nil {
				return spiffeid.TrustDomain{}, err
			}
			log.Warn("WARNING: Sentry accepted a token with the audience for the Kubernetes API server. This is deprecated and only supported to ensure a smooth upgrade from Dapr pre-1.10.")
		} else {
			return spiffeid.TrustDomain{}, err
		}
	}

	if len(prts) != 4 || prts[0] != "system" {
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: provided token is not a properly structured service account token", errPrefix)
	}

	if prts[2] != req.Namespace {
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: namespace mismatch. received namespace: %s",
			errPrefix, req.Namespace)
	}

	var claims k8sClaims
	// We have already validated to the token against Kubernetes API server, so
	// we do not need to supply a key.
	ptoken, _, err := jwt.NewParser().ParseUnverified(req.GetToken(), &claims)
	if err != nil {
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: failed to parse kubernetes token: %s", errPrefix, err)
	}
	if err := ptoken.Claims.Valid(); err != nil {
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: invalid kubernetes token: %s", errPrefix, err)
	}

	var pod corev1.Pod
	err = k.client.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: claims.Kubernetes.Pod.Name}, &pod)
	if err != nil {
		log.Errorf("failed to get pod %s/%s for requested identity: %s", req.Namespace, claims.Kubernetes.Pod.Name, err)
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: failed to get pod of identity", errPrefix)
	}
	expID, ok := pod.GetAnnotations()[annotations.KeyAppID]
	if !ok {
		expID = pod.GetName()
	}

	if pod.Spec.ServiceAccountName != prts[3] {
		log.Errorf("service account on pod %s/%s does not match token", req.Namespace, claims.Kubernetes.Pod.Name)
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: pod service account mismatch", errPrefix)
	}

	// TODO: @joshvanl: Before v1.11, the injector instructed daprd to request
	// for the ID containing their namespace and service account (ns:sa). This
	// is wrong- dapr identities are based on daprd namespace + _app ID_.
	// Remove this allowance in v1.12.
	if fmt.Sprintf(req.Namespace+":"+pod.Spec.ServiceAccountName) == req.Id {
		req.Id = expID
	}

	if expID != req.Id {
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: app-id mismatch. expected: %s, received: %s",
			errPrefix, expID, req.Id)
	}

	configName, ok := pod.GetAnnotations()[annotations.KeyConfig]
	if !ok {
		if k.isControlPlaneComponent(req.Namespace, req.Id) {
			return k.controlPlaneTD, nil
		}

		// Return early with default trust domain if no config annotation is found.
		return spiffeid.RequireTrustDomainFromString("public"), nil
	}

	var config configv1alpha1.Configuration
	err = k.client.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: configName}, &config)
	if err != nil {
		log.Errorf("failed to get configuration %q: %s", configName, err)
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: failed to get configuration", errPrefix)
	}

	if len(config.Spec.AccessControlSpec.TrustDomain) == 0 {
		return spiffeid.RequireTrustDomainFromString("public"), nil
	}

	return spiffeid.TrustDomainFromString(config.Spec.AccessControlSpec.TrustDomain)
}

// Executes a tokenReview, returning an error if the token is invalid or if
// there's a failure.
// If successful, returns the username of the token, split by the Kubernetes
// ':' separator.
func (k *kubernetes) executeTokenReview(ctx context.Context, token string, audiences ...string) ([]string, error) {
	review, err := k.auth.TokenReviews().Create(ctx, &kauthapi.TokenReview{
		Spec: kauthapi.TokenReviewSpec{Token: token, Audiences: audiences},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("%s: token review failed: %w", errPrefix, err)
	}

	if len(review.Status.Error) > 0 {
		return nil, fmt.Errorf("%s: invalid token: %s", errPrefix, review.Status.Error)
	}

	if !review.Status.Authenticated {
		return nil, fmt.Errorf("%s: authentication failed", errPrefix)
	}

	return strings.Split(review.Status.User.Username, ":"), nil
}

// k8sClaims is a subset of the claims in a Kubernetes service account token
// containing the name of the Pod that the token was issued for.
type k8sClaims struct {
	Kubernetes struct {
		Pod struct {
			Name string `json:"name"`
		} `json:"pod"`
	} `json:"kubernetes.io"`
}

// Valid implements the jwt.Claims interface.
func (k *k8sClaims) Valid() error {
	if len(k.Kubernetes.Pod.Name) == 0 {
		return errors.New("kubernetes.io/pod/name claim is missing")
	}
	return nil
}

func (k *kubernetes) isControlPlaneComponent(ns string, appID string) bool {
	if ns != k.controlPlaneNS {
		return false
	}

	for _, ctrl := range []string{
		"dapr-operator",
		"dapr-placement",
		"dapr-injector",
		"dapr-sentry",
	} {
		if appID == ctrl {
			return true
		}
	}
	return false
}
