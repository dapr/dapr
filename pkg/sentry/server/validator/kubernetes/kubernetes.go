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
	"math"
	"strings"

	"github.com/lestrrat-go/jwx/v2/jwt"
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
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/injector/annotations"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
	"github.com/dapr/dapr/pkg/sentry/server/validator/internal"
	"github.com/dapr/kit/logger"
)

var (
	log = logger.NewLogger("dapr.sentry.identity.kubernetes")

	errMissingPodClaim = errors.New("kubernetes.io/pod/name claim is missing from Kubernetes token")
)

const (
	// TODO: @joshvanl: Before 1.12, dapr would use this generic audience. After
	// 1.13, clients use the sentry SPIFFE ID as the audience. Remove this
	// constant in 1.14.
	LegacyServiceAccountAudience = "dapr.io/sentry"
)

type Options struct {
	RestConfig     *rest.Config
	SentryID       spiffeid.ID
	ControlPlaneNS string
	Healthz        healthz.Healthz
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
	htarget        healthz.Target
}

func New(opts Options) (validator.Validator, error) {
	kubeClient, err := cl.NewForConfig(opts.RestConfig)
	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	if err = configv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err = corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	opts.RestConfig.RateLimiter = nil
	opts.RestConfig.QPS = math.MaxFloat32
	opts.RestConfig.Burst = math.MaxInt
	cache, err := cache.New(opts.RestConfig, cache.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	return &kubernetes{
		auth:           kubeClient.AuthenticationV1(),
		client:         cache,
		ready:          cache.WaitForCacheSync,
		sentryAudience: opts.SentryID.String(),
		controlPlaneNS: opts.ControlPlaneNS,
		controlPlaneTD: opts.SentryID.TrustDomain(),
		htarget:        opts.Healthz.AddTarget("sentry-kubernetes-validator"),
	}, nil
}

func (k *kubernetes) Start(ctx context.Context) error {
	defer k.htarget.NotReady()

	cache := k.client.(cache.Cache)
	for _, obj := range []client.Object{
		&metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"}},
		&configv1alpha1.Configuration{},
	} {
		if _, err := cache.GetInformer(ctx, obj); err != nil {
			return err
		}
	}

	go func() {
		if k.ready(ctx) {
			k.htarget.Ready()
		}
	}()

	if err := cache.Start(ctx); err != nil {
		return err
	}

	return nil
}

func (k *kubernetes) Validate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error) {
	if !k.ready(ctx) {
		return validator.ValidateResult{}, errors.New("validator not ready")
	}

	// The TrustDomain field is ignored by the Kubernetes validator.
	if _, err := internal.Validate(ctx, req); err != nil {
		return validator.ValidateResult{}, err
	}

	prts, err := k.executeTokenReview(ctx, req.GetToken(), LegacyServiceAccountAudience, k.sentryAudience)
	if err != nil {
		return validator.ValidateResult{}, err
	}

	if len(prts) != 4 || prts[0] != "system" {
		return validator.ValidateResult{}, errors.New("provided token is not a properly structured service account token")
	}

	saNamespace := prts[2]

	// We have already validated to the token against Kubernetes API server, so
	// we do not need to supply a key.
	ptoken, err := jwt.ParseInsecure([]byte(req.GetToken()), jwt.WithTypedClaim("kubernetes.io", new(k8sClaims)))
	if err != nil {
		return validator.ValidateResult{}, fmt.Errorf("failed to parse Kubernetes token: %s", err)
	}
	claimsT, ok := ptoken.Get("kubernetes.io")
	if !ok {
		return validator.ValidateResult{}, errMissingPodClaim
	}
	claims, ok := claimsT.(*k8sClaims)
	if !ok || len(claims.Pod.Name) == 0 {
		return validator.ValidateResult{}, errMissingPodClaim
	}

	var pod corev1.Pod
	err = k.client.Get(ctx, types.NamespacedName{Namespace: saNamespace, Name: claims.Pod.Name}, &pod)
	if err != nil {
		log.Errorf("Failed to get pod %s/%s for requested identity: %s", saNamespace, claims.Pod.Name, err)
		return validator.ValidateResult{}, errors.New("failed to get pod of identity")
	}

	if saNamespace != req.GetNamespace() {
		return validator.ValidateResult{}, fmt.Errorf("namespace mismatch; received namespace: %s", req.GetNamespace())
	}

	if pod.Spec.ServiceAccountName != prts[3] {
		log.Errorf("Service account on pod %s/%s does not match token", req.GetNamespace(), claims.Pod.Name)
		return validator.ValidateResult{}, errors.New("pod service account mismatch")
	}

	expID, isControlPlane, err := k.expectedID(&pod)
	if err != nil {
		log.Errorf("Failed to get expected ID for pod %s/%s: %s", req.GetNamespace(), claims.Pod.Name, err)
		return validator.ValidateResult{}, err
	}

	if expID != req.GetId() {
		return validator.ValidateResult{}, fmt.Errorf("app-id mismatch. expected: %s, received: %s", expID, req.GetId())
	}

	if isControlPlane {
		return validator.ValidateResult{
			TrustDomain: k.controlPlaneTD,
		}, nil
	}

	configName, ok := pod.GetAnnotations()[annotations.KeyConfig]
	if !ok {
		// Return early with default trust domain if no config annotation is found.
		return validator.ValidateResult{
			TrustDomain: spiffeid.RequireTrustDomainFromString("public"),
		}, nil
	}

	var config configv1alpha1.Configuration
	err = k.client.Get(ctx, types.NamespacedName{Namespace: req.GetNamespace(), Name: configName}, &config)
	if err != nil {
		log.Errorf("Failed to get configuration %q: %v", configName, err)
		return validator.ValidateResult{}, errors.New("failed to get configuration")
	}

	if config.Spec.AccessControlSpec == nil || len(config.Spec.AccessControlSpec.TrustDomain) == 0 {
		return validator.ValidateResult{
			TrustDomain: spiffeid.RequireTrustDomainFromString("public"),
		}, nil
	}

	td, err := spiffeid.TrustDomainFromString(config.Spec.AccessControlSpec.TrustDomain)
	if err != nil {
		return validator.ValidateResult{}, fmt.Errorf("failed to parse trust domain %q: %w", config.Spec.AccessControlSpec.TrustDomain, err)
	}
	return validator.ValidateResult{
		TrustDomain: td,
	}, nil
}

// expectedID returns the expected ID for the pod. If the pod is a control
// plane service (has the dapr.io/control-plane annotation), the ID will be the
// control plane service name prefixed with "dapr-". Otherwise, the ID will be
// `dapr.io/app-id` annotation or the pod name.
func (k *kubernetes) expectedID(pod *corev1.Pod) (string, bool, error) {
	ctrlPlane, ctrlOK := pod.Annotations[consts.AnnotationKeyControlPlane]
	appID, appOK := pod.Annotations[annotations.KeyAppID]

	if ctrlOK {
		if pod.Namespace != k.controlPlaneNS {
			return "", false, fmt.Errorf("control plane service in namespace '%s' is not allowed", pod.Namespace)
		}

		if !isControlPlaneService(ctrlPlane) {
			return "", false, fmt.Errorf("unknown control plane service '%s'", pod.Name)
		}

		if appOK {
			return "", false, fmt.Errorf("control plane service '%s' cannot have annotation '%s'", pod.Name, annotations.KeyAppID)
		}

		return "dapr-" + ctrlPlane, true, nil
	}

	if !appOK {
		return pod.Name, false, nil
	}

	return appID, false, nil
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
		return nil, fmt.Errorf("token review failed: %w", err)
	}

	if len(review.Status.Error) > 0 {
		return nil, fmt.Errorf("invalid token: %s", review.Status.Error)
	}

	if !review.Status.Authenticated {
		return nil, errors.New("authentication failed")
	}

	return strings.Split(review.Status.User.Username, ":"), nil
}

// k8sClaims is a subset of the claims in a Kubernetes service account token
// containing the name of the Pod that the token was issued for.
type k8sClaims struct {
	Pod struct {
		Name string `json:"name"`
	} `json:"pod"`
}

// IsControlPlaneService returns true if the app ID corresponds to a Dapr control plane service.
// Note: callers must additionally validate the namespace to ensure it matches the one of the Dapr control plane.
func isControlPlaneService(id string) bool {
	switch id {
	case "operator",
		"placement",
		"injector",
		"sentry",
		"scheduler":
		return true
	default:
		return false
	}
}
