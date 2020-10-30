// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package injector

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/injector/monitoring"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/pkg/errors"
	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
)

const port = 4000

var log = logger.NewLogger("dapr.injector")

// Injector is the interface for the Dapr runtime sidecar injection component
type Injector interface {
	Run(ctx context.Context)
}

type injector struct {
	config       Config
	deserializer runtime.Decoder
	server       *http.Server
	kubeClient   *kubernetes.Clientset
	daprClient   scheme.Interface
	authUID      string
}

// toAdmissionResponse is a helper function to create an AdmissionResponse
// with an embedded error
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

// NewInjector returns a new Injector instance with the given config
func NewInjector(authUID string, config Config, daprClient scheme.Interface, kubeClient *kubernetes.Clientset) Injector {
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
		authUID:    authUID,
	}

	mux.HandleFunc("/mutate", i.handleRequest)
	return i
}

func ReplicasetAccountUID(kubeClient *kubernetes.Clientset) (string, error) {
	r, err := kubeClient.CoreV1().ServiceAccounts(metav1.NamespaceSystem).Get("replicaset-controller", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return string(r.ObjectMeta.UID), nil
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
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	if len(body) == 0 {
		log.Error("empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		log.Errorf("Content-Type=%s, expect application/json", contentType)
		http.Error(
			w,
			"invalid Content-Type, expect `application/json`",
			http.StatusUnsupportedMediaType,
		)

		return
	}

	var admissionResponse *v1.AdmissionResponse
	var patchOps []PatchOperation
	var err error

	ar := v1.AdmissionReview{}
	_, gvk, err := i.deserializer.Decode(body, nil, &ar)
	if err != nil {
		log.Errorf("Can't decode body: %v", err)
	} else {
		if ar.Request.UserInfo.UID != i.authUID {
			err = errors.Wrapf(err, "unauthorized request")
			log.Error(err)
		} else if ar.Request.Kind.Kind != "Pod" {
			err = errors.Wrapf(err, "invalid kind for review: %s", ar.Kind)
			log.Error(err)
		} else {
			patchOps, err = i.getPodPatchOperations(&ar, i.config.Namespace, i.config.SidecarImage, i.kubeClient, i.daprClient)
		}
	}

	diagAppID := getAppIDFromRequest(ar.Request)

	if err != nil {
		admissionResponse = toAdmissionResponse(err)
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
		log.Errorf("can't deserialize response: %s", err)

		http.Error(
			w,
			err.Error(),
			http.StatusInternalServerError,
		)

		monitoring.RecordFailedSidecarInjectionCount(diagAppID, "response")
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(respBytes); err != nil {
		log.Error(err)
	} else {
		monitoring.RecordSuccessfulSidecarInjectionCount(diagAppID)
	}
}
