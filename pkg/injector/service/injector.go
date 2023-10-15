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

package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/namespacednamematcher"
	"github.com/dapr/kit/logger"
)

const (
	port                                      = 4000
	getKubernetesServiceAccountTimeoutSeconds = 10
	systemGroup                               = "system:masters"
	serviceAccountUserInfoPrefix              = "system:serviceaccount:"
)

var log = logger.NewLogger("dapr.injector.service")

var AllowedServiceAccountInfos = []string{
	"kube-system:replicaset-controller",
	"kube-system:deployment-controller",
	"kube-system:cronjob-controller",
	"kube-system:job-controller",
	"kube-system:statefulset-controller",
	"kube-system:daemon-set-controller",
	"tekton-pipelines:tekton-pipelines-controller",
}

type (
	signDaprdCertificateFn func(ctx context.Context, namespace string) (cert []byte, key []byte, err error)
	currentTrustAnchorsFn  func() (ca []byte, err error)
)

// Injector is the interface for the Dapr runtime sidecar injection component.
type Injector interface {
	Run(context.Context, *tls.Config, spiffeid.ID, signDaprdCertificateFn, currentTrustAnchorsFn) error
	Ready(context.Context) error
}

type Options struct {
	AuthUIDs   []string
	Config     Config
	DaprClient scheme.Interface
	KubeClient kubernetes.Interface

	ControlPlaneNamespace   string
	ControlPlaneTrustDomain string
}

type injector struct {
	config       Config
	deserializer runtime.Decoder
	server       *http.Server
	kubeClient   kubernetes.Interface
	daprClient   scheme.Interface
	authUIDs     []string

	controlPlaneNamespace   string
	controlPlaneTrustDomain string
	currentTrustAnchors     currentTrustAnchorsFn
	sentrySPIFFEID          spiffeid.ID
	signDaprdCertificate    signDaprdCertificateFn

	namespaceNameMatcher *namespacednamematcher.EqualPrefixNameNamespaceMatcher
	ready                chan struct{}
}

// errorToAdmissionResponse is a helper function to create an AdmissionResponse
// with an embedded error.
func errorToAdmissionResponse(err error) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}

// getAppIDFromRequest returns the app ID for the pod, which is used for diagnostics purposes only
func getAppIDFromRequest(req *admissionv1.AdmissionRequest) (appID string) {
	if req == nil {
		return ""
	}

	// Parse the request as a pod
	var pod corev1.Pod
	err := json.Unmarshal(req.Object.Raw, &pod)
	if err != nil {
		log.Warnf("could not unmarshal raw object: %v", err)
		return ""
	}

	// Search for an app-id in the annotations first
	for k, v := range pod.GetObjectMeta().GetAnnotations() {
		if k == annotations.KeyAppID {
			return v
		}
	}

	// Fallback to pod name
	return pod.GetName()
}

// NewInjector returns a new Injector instance with the given config.
func NewInjector(opts Options) (Injector, error) {
	mux := http.NewServeMux()

	i := &injector{
		config: opts.Config,
		deserializer: serializer.NewCodecFactory(
			runtime.NewScheme(),
		).UniversalDeserializer(),
		server: &http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		},
		kubeClient:              opts.KubeClient,
		daprClient:              opts.DaprClient,
		authUIDs:                opts.AuthUIDs,
		controlPlaneNamespace:   opts.ControlPlaneNamespace,
		controlPlaneTrustDomain: opts.ControlPlaneTrustDomain,
		ready:                   make(chan struct{}),
	}

	matcher, err := createNamespaceNameMatcher(opts.Config.AllowedServiceAccountsPrefixNames)
	if err != nil {
		return nil, err
	}
	i.namespaceNameMatcher = matcher

	mux.HandleFunc("/mutate", i.handleRequest)
	return i, nil
}

func createNamespaceNameMatcher(allowedPrefix string) (matcher *namespacednamematcher.EqualPrefixNameNamespaceMatcher, err error) {
	allowedPrefix = strings.TrimSpace(allowedPrefix)
	if allowedPrefix != "" {
		matcher, err = namespacednamematcher.CreateFromString(allowedPrefix)
		if err != nil {
			return nil, err
		}
		log.Debugf("Sidecar injector configured to allowed serviceaccounts prefixed by: %s", allowedPrefix)
	}
	return matcher, nil
}

// AllowedControllersServiceAccountUID returns an array of UID, list of allowed service account on the webhook handler.
func AllowedControllersServiceAccountUID(ctx context.Context, cfg Config, kubeClient kubernetes.Interface) ([]string, error) {
	allowedList := []string{}
	if cfg.AllowedServiceAccounts != "" {
		allowedList = append(allowedList, strings.Split(cfg.AllowedServiceAccounts, ",")...)
	}
	allowedList = append(allowedList, AllowedServiceAccountInfos...)

	return getServiceAccount(ctx, kubeClient, allowedList)
}

// getServiceAccount parses "service-account:namespace" k/v list and returns an array of UID.
func getServiceAccount(ctx context.Context, kubeClient kubernetes.Interface, allowedServiceAccountInfos []string) ([]string, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, getKubernetesServiceAccountTimeoutSeconds*time.Second)
	defer cancel()

	serviceaccounts, err := kubeClient.CoreV1().ServiceAccounts("").List(ctxWithTimeout, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	allowedUids := []string{}

	for _, allowedServiceInfo := range allowedServiceAccountInfos {
		serviceAccountInfo := strings.Split(allowedServiceInfo, ":")
		found := false
		for _, sa := range serviceaccounts.Items {
			if sa.Namespace == serviceAccountInfo[0] && sa.Name == serviceAccountInfo[1] {
				allowedUids = append(allowedUids, string(sa.ObjectMeta.UID))
				found = true
				break
			}
		}
		if !found {
			log.Warnf("Unable to get SA %s UID", allowedServiceInfo)
		}
	}

	return allowedUids, nil
}

func (i *injector) Run(ctx context.Context, tlsConfig *tls.Config, sentryID spiffeid.ID, signDaprdFn signDaprdCertificateFn, currentTrustAnchors currentTrustAnchorsFn) error {
	select {
	case <-i.ready:
		return errors.New("injector already running")
	default:
		// Nop
	}

	log.Infof("Sidecar injector is listening on %s, patching Dapr-enabled pods", i.server.Addr)

	i.currentTrustAnchors = currentTrustAnchors
	i.signDaprdCertificate = signDaprdFn
	i.sentrySPIFFEID = sentryID
	i.server.TLSConfig = tlsConfig

	errCh := make(chan error, 1)
	go func() {
		err := i.server.ListenAndServeTLS("", "")
		if !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("sidecar injector error: %w", err)
			return
		}
		errCh <- nil
	}()

	close(i.ready)

	select {
	case <-ctx.Done():
		log.Info("Sidecar injector is shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := i.server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("error while shutting down injector: %v; %v", err, <-errCh)
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}

func (i *injector) Ready(ctx context.Context) error {
	select {
	case <-i.ready:
		return nil
	case <-ctx.Done():
		return errors.New("timed out waiting for injector to become ready")
	}
}
