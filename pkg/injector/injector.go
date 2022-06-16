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
	"net"
	"net/http"
	"strings"
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

var AllowedServiceAccountInfos = []string{
	"replicaset-controller:kube-system",
	"deployment-controller:kube-system",
	"cronjob-controller:kube-system",
	"job-controller:kube-system",
	"statefulset-controller:kube-system",
	"daemon-set-controller:kube-system",
	"tekton-pipelines-controller:tekton-pipelines",
}

// Injector is the interface for the Dapr runtime sidecar injection component.
type Injector interface {
	Run(ctx context.Context, onReady func())
}

type injector struct {
	config       Config
	deserializer runtime.Decoder
	server       *http.Server
	kubeClient   kubernetes.Interface
	daprClient   scheme.Interface
	authUIDs     []string
}

// errorToAdmissionResponse is a helper function to create an AdmissionResponse
// with an embedded error.
func errorToAdmissionResponse(err error) *v1.AdmissionResponse {
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
func AllowedControllersServiceAccountUID(ctx context.Context, cfg Config, kubeClient kubernetes.Interface) ([]string, error) {
	allowedList := strings.Split(cfg.AllowedServiceAccounts, ",")
	allowedList = append(allowedList, AllowedServiceAccountInfos...)

	return getServiceAccount(ctx, kubeClient, allowedList)
}

// getServiceAccount parses "service-account:namespace" k/v list and returns an array of UID.
func getServiceAccount(ctx context.Context, kubeClient kubernetes.Interface, allowedServiceAcccountInfos []string) ([]string, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, getKubernetesServiceAccountTimeoutSeconds*time.Second)
	defer cancel()

	serviceaccounts, err := kubeClient.CoreV1().ServiceAccounts("").List(ctxWithTimeout, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	allowedUids := []string{}

	for _, allowedServiceInfo := range allowedServiceAcccountInfos {
		serviceAccountInfo := strings.Split(allowedServiceInfo, ":")
		for _, sa := range serviceaccounts.Items {
			if sa.Name == serviceAccountInfo[0] && sa.Namespace == serviceAccountInfo[1] {
				allowedUids = append(allowedUids, string(sa.ObjectMeta.UID))
				break
			}
		}
		log.Warnf("Unable to get SA %s UID (%s)", allowedServiceInfo, err)
	}

	return allowedUids, nil
}

func (i *injector) Run(ctx context.Context, onReady func()) {
	go func() {
		select {
		case <-ctx.Done():
			log.Info("Sidecar injector is shutting down")
			shutdownCtx, cancel := context.WithTimeout(
				context.Background(),
				time.Second*5,
			)
			defer cancel()
			err := i.server.Shutdown(shutdownCtx)
			if err != nil {
				log.Errorf("Error while shutting down injector: %v", err)
			}
		}
	}()

	ln, err := net.Listen("tcp", i.server.Addr)
	if err != nil {
		log.Fatalf("Eror while creating listener: %v", err)
	}

	log.Infof("Sidecar injector is listening on %s, patching Dapr-enabled pods", i.server.Addr)

	if onReady != nil {
		onReady()
	}

	err = i.server.ServeTLS(ln, i.config.TLSCertFile, i.config.TLSKeyFile)
	if err != http.ErrServerClosed {
		log.Errorf("Sidecar injector error: %s", err)
	}

	ln.Close()

	log.Info("Sidecar injector stopped")
}

func (i *injector) handleRequest(w http.ResponseWriter, r *http.Request) {
	monitoring.RecordSidecarInjectionRequestsCount()

	var body []byte
	var err error
	if r.Body != nil {
		defer r.Body.Close()
		body, err = io.ReadAll(r.Body)
		if err != nil {
			body = nil
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
		errStr := fmt.Sprintf("invalid Content-Type, expected `%s`", runtime.ContentTypeJSON)
		http.Error(w, errStr, http.StatusUnsupportedMediaType)
		return
	}

	var patchOps []PatchOperation
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

	var admissionResponse *v1.AdmissionResponse
	if err != nil {
		admissionResponse = errorToAdmissionResponse(err)
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
			admissionResponse = errorToAdmissionResponse(err)
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

	admissionReview := v1.AdmissionReview{
		Response: admissionResponse,
	}
	if admissionResponse != nil && ar.Request != nil {
		admissionReview.Response.UID = ar.Request.UID
		admissionReview.SetGroupVersionKind(*gvk)
	}

	// log.Debug("ready to write response ...")

	respBytes, err := json.Marshal(admissionReview)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Errorf("Sidecar injector failed to inject for app '%s'. Can't serialize response: %s", diagAppID, err)
		monitoring.RecordFailedSidecarInjectionCount(diagAppID, "response")
		return
	}
	w.Header().Set("Content-Type", runtime.ContentTypeJSON)
	_, err = w.Write(respBytes)
	if err != nil {
		log.Errorf("Sidecar injector failed to inject for app '%s'. Failed to write response: %v", diagAppID, err)
		monitoring.RecordFailedSidecarInjectionCount(diagAppID, "write_response")
		return
	}

	if patchedSuccessfully {
		log.Infof("Sidecar injector succeeded injection for app '%s'", diagAppID)
		monitoring.RecordSuccessfulSidecarInjectionCount(diagAppID)
	} else {
		log.Errorf("Admission succeeded, but pod was not patched. No sidecar injected for '%s'", diagAppID)
		monitoring.RecordFailedSidecarInjectionCount(diagAppID, "pod_patch")
	}
}
