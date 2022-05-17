/*
Copyright 2021 The Dapr Authors
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

package injector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"

	"github.com/dapr/kit/logger"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/injector/monitoring"
	"github.com/dapr/dapr/utils"
)

const (
	port                                      = 4000
	getKubernetesServiceAccountTimeoutSeconds = 10
	systemGroup                               = "system:masters"
)

var log = logger.NewLogger("dapr.injector")

var allowedControllersServiceAccounts = []string{
	"replicaset-controller",
	"deployment-controller",
	"cronjob-controller",
	"job-controller",
	"statefulset-controller",
	"daemon-set-controller",
	"tekton-pipelines-controller",
}

// Injector is the interface for the Dapr runtime sidecar injection component.
type Injector interface {
	Run(ctx context.Context)
}

type injector struct {
	config       Config
	deserializer runtime.Decoder
	server       *http.Server
	kubeClient   kubernetes.Interface
	daprClient   scheme.Interface
	authUIDs     []string
}

// toAdmissionResponse is a helper function to create an AdmissionResponse
// with an embedded error.
func toAdmissionResponse(err error) *v1.AdmissionResponse {
	return &v1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}

func getAppIDFromRequest(req *v1.AdmissionRequest) string {
	// default App ID
	appID := ""

	// if req is not given
	if req == nil {
		return appID
	}

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		log.Warnf("could not unmarshal raw object: %v", err)
	} else {
		appID = getAppID(pod)
	}

	return appID
}

// NewInjector returns a new Injector instance with the given config.
func NewInjector(authUIDs []string, config Config, daprClient scheme.Interface, kubeClient kubernetes.Interface) Injector {
	mux := http.NewServeMux()

	i := &injector{
		config: config,
		deserializer: serializer.NewCodecFactory(
			runtime.NewScheme(),
		).UniversalDeserializer(),
		server: &http.Server{
			Addr:    fmt.Sprintf(":%d", port),
			Handler: mux,
		},
		kubeClient: kubeClient,
		daprClient: daprClient,
		authUIDs:   authUIDs,
	}

	mux.HandleFunc("/mutate", i.handleRequest)
	return i
}

// AllowedControllersServiceAccountUID returns an array of UID, list of allowed service account on the webhook handler.
func AllowedControllersServiceAccountUID(ctx context.Context, kubeClient kubernetes.Interface) ([]string, error) {
	allowedUids := []string{}
	for i, allowedControllersServiceAccount := range allowedControllersServiceAccounts {
		saUUID, err := getServiceAccount(ctx, kubeClient, allowedControllersServiceAccount)
		// i == 0 => "replicaset-controller" is the only one mandatory
		if err != nil {
			if i == 0 {
				return nil, err
			}
			log.Warnf("Unable to get SA %s UID (%s)", allowedControllersServiceAccount, err)
			continue
		}
		allowedUids = append(allowedUids, saUUID)
	}

	return allowedUids, nil
}

func getServiceAccount(ctx context.Context, kubeClient kubernetes.Interface, allowedControllersServiceAccount string) (string, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, getKubernetesServiceAccountTimeoutSeconds*time.Second)
	defer cancel()

	sa, err := kubeClient.CoreV1().ServiceAccounts(metav1.NamespaceSystem).Get(ctxWithTimeout, allowedControllersServiceAccount, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	return string(sa.ObjectMeta.UID), nil
}

func (i *injector) Run(ctx context.Context) {
	doneCh := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
			log.Info("Sidecar injector is shutting down")
			shutdownCtx, cancel := context.WithTimeout(
				context.Background(),
				time.Second*5,
			)
			defer cancel()
			i.server.Shutdown(shutdownCtx) // nolint: errcheck
		case <-doneCh:
		}
	}()

	log.Infof("Sidecar injector is listening on %s, patching Dapr-enabled pods", i.server.Addr)
	err := i.server.ListenAndServeTLS(i.config.TLSCertFile, i.config.TLSKeyFile)
	if err != http.ErrServerClosed {
		log.Errorf("Sidecar injector error: %s", err)
	}
	close(doneCh)
}

func (i *injector) handleRequest(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	monitoring.RecordSidecarInjectionRequestsCount()

	var body []byte
	if r.Body != nil {
		if data, err := io.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	if len(body) == 0 {
		log.Error("empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType != runtime.ContentTypeJSON {
		log.Errorf("Content-Type=%s, expect %s", contentType, runtime.ContentTypeJSON)
		http.Error(
			w,
			fmt.Sprintf("invalid Content-Type, expect `%s`", runtime.ContentTypeJSON),
			http.StatusUnsupportedMediaType,
		)

		return
	}

	var admissionResponse *v1.AdmissionResponse
	var patchOps []PatchOperation
	var err error
	patchedSuccessfully := false

	ar := v1.AdmissionReview{}
	_, gvk, err := i.deserializer.Decode(body, nil, &ar)
	if err != nil {
		log.Errorf("Can't decode body: %v", err)
	} else {
		if !(utils.StringSliceContains(ar.Request.UserInfo.UID, i.authUIDs) || utils.StringSliceContains(systemGroup, ar.Request.UserInfo.Groups)) {
			log.Errorf("service account '%s' not on the list of allowed controller accounts", ar.Request.UserInfo.Username)
		} else if ar.Request.Kind.Kind != "Pod" {
			log.Errorf("invalid kind for review: %s", ar.Kind)
		} else {
			patchOps, err = i.getPodPatchOperations(&ar, i.config.Namespace, i.config.SidecarImage, i.config.SidecarImagePullPolicy, i.kubeClient, i.daprClient)
			if err == nil {
				patchedSuccessfully = true
			}
		}
	}

	diagAppID := getAppIDFromRequest(ar.Request)

	if err != nil {
		admissionResponse = toAdmissionResponse(err)
		log.Errorf("Sidecar injector failed to inject for app '%s'. Error: %s", diagAppID, err)
		monitoring.RecordFailedSidecarInjectionCount(diagAppID, "patch")
	} else if len(patchOps) == 0 {
		admissionResponse = &v1.AdmissionResponse{
			Allowed: true,
		}
	} else {
		var patchBytes []byte
		patchBytes, err = json.Marshal(patchOps)
		if err != nil {
			admissionResponse = toAdmissionResponse(err)
		} else {
			admissionResponse = &v1.AdmissionResponse{
				Allowed: true,
				Patch:   patchBytes,
				PatchType: func() *v1.PatchType {
					pt := v1.PatchTypeJSONPatch
					return &pt
				}(),
			}
		}
	}

	admissionReview := v1.AdmissionReview{}
	if admissionResponse != nil {
		admissionReview.Response = admissionResponse
		if ar.Request != nil {
			admissionReview.Response.UID = ar.Request.UID
			admissionReview.SetGroupVersionKind(*gvk)
		}
	}

	log.Infof("ready to write response ...")
	respBytes, err := json.Marshal(admissionReview)
	if err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusInternalServerError,
		)

		log.Errorf("Sidecar injector failed to inject for app '%s'. Can't deserialize response: %s", diagAppID, err)
		monitoring.RecordFailedSidecarInjectionCount(diagAppID, "response")
	}
	w.Header().Set("Content-Type", runtime.ContentTypeJSON)
	if _, err := w.Write(respBytes); err != nil {
		log.Error(err)
	} else {
		if patchedSuccessfully {
			log.Infof("Sidecar injector succeeded injection for app '%s'", diagAppID)
			monitoring.RecordSuccessfulSidecarInjectionCount(diagAppID)
		} else {
			log.Errorf("Admission succeeded, but pod was not patched. No sidecar injected for '%s'", diagAppID)
			monitoring.RecordFailedSidecarInjectionCount(diagAppID, "pod_patch")
		}
	}
}
