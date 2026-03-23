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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/utils"
)

func (i *injector) handleRequest(w http.ResponseWriter, r *http.Request) {
	RecordSidecarInjectionRequestsCount()

	body, err := readRequestBody(r)
	if err != nil {
		log.Error("Empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	if ct := r.Header.Get("Content-Type"); ct != runtime.ContentTypeJSON {
		log.Errorf("Content-Type=%s, expect %s", ct, runtime.ContentTypeJSON)
		http.Error(w, fmt.Sprintf("invalid Content-Type, expected `%s`", runtime.ContentTypeJSON), http.StatusUnsupportedMediaType)
		return
	}

	ar := admissionv1.AdmissionReview{}
	_, gvk, err := i.deserializer.Decode(body, nil, &ar)
	if err != nil {
		log.Errorf("Can't decode body: %v", err)
		i.writeAdmissionResponse(w, ar, gvk, nil, err)
		return
	}

	if ar.Request.Kind.Kind != "Pod" {
		log.Errorf("invalid kind for review: %s", ar.Request.Kind.Kind)
		diagAppID := getAppIDFromRequest(ar.Request)
		respondWithAllowed(w, ar, gvk)
		RecordFailedSidecarInjectionCount(diagAppID, "pod_patch")
		return
	}

	// Non-Dapr pods are silently allowed without any metrics or logging.
	if !podHasDaprEnabled(ar.Request) {
		respondWithAllowed(w, ar, gvk)
		return
	}

	if !i.isAuthorizedUser(ar.Request) {
		log.Errorf("service account '%s' not on the list of allowed controller accounts", ar.Request.UserInfo.Username)
		diagAppID := getAppIDFromRequest(ar.Request)
		respondWithAllowed(w, ar, gvk)
		RecordFailedSidecarInjectionCount(diagAppID, "pod_patch")
		return
	}

	patchOps, err := i.getPodPatchOperations(r.Context(), &ar)
	i.writeAdmissionResponse(w, ar, gvk, patchOps, err)
}

// readRequestBody reads the full request body and returns it. Returns an error
// if the body is nil, unreadable, or empty.
func readRequestBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, fmt.Errorf("empty body")
	}
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 {
		return nil, fmt.Errorf("empty body")
	}
	return body, nil
}

// isAuthorizedUser checks whether the admission request was made by an
// authorized service account, UID, or group.
func (i *injector) isAuthorizedUser(req *admissionv1.AdmissionRequest) bool {
	return i.allowServiceAccountUser(req.UserInfo.Username) ||
		utils.Contains(i.authUIDs, req.UserInfo.UID) ||
		utils.Contains(req.UserInfo.Groups, systemGroup)
}

// writeAdmissionResponse builds an AdmissionReview response from the patch
// result, writes it to w, and records the appropriate injection metrics.
func (i *injector) writeAdmissionResponse(w http.ResponseWriter, ar admissionv1.AdmissionReview, gvk *schema.GroupVersionKind, patchOps jsonpatch.Patch, patchErr error) {
	diagAppID := getAppIDFromRequest(ar.Request)

	resp, buildErr := buildAdmissionResponse(patchOps, patchErr)

	review := admissionv1.AdmissionReview{Response: resp}
	if resp != nil && ar.Request != nil {
		review.Response.UID = ar.Request.UID
	}
	if gvk != nil {
		review.SetGroupVersionKind(*gvk)
	}

	respBytes, err := json.Marshal(review)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Errorf("Sidecar injector failed to inject for app '%s'. Can't serialize response: %s", diagAppID, err)
		RecordFailedSidecarInjectionCount(diagAppID, "response")
		return
	}

	w.Header().Set("Content-Type", runtime.ContentTypeJSON)
	if _, err = w.Write(respBytes); err != nil {
		log.Errorf("Sidecar injector failed to inject for app '%s'. Failed to write response: %v", diagAppID, err)
		RecordFailedSidecarInjectionCount(diagAppID, "write_response")
		return
	}

	// Record injection metrics.
	switch {
	case buildErr != nil:
		log.Errorf("Sidecar injector failed to inject for app '%s'. Error: %s", diagAppID, buildErr)
		RecordFailedSidecarInjectionCount(diagAppID, "patch")
	case len(patchOps) > 0:
		log.Infof("Sidecar injector succeeded injection for app '%s'", diagAppID)
		RecordSuccessfulSidecarInjectionCount(diagAppID)
	}
}

// buildAdmissionResponse creates an AdmissionResponse from patch operations.
// Returns the response and any error encountered during building (including
// patch marshaling failures).
func buildAdmissionResponse(patchOps jsonpatch.Patch, patchErr error) (*admissionv1.AdmissionResponse, error) {
	if patchErr != nil {
		return errorToAdmissionResponse(patchErr), patchErr
	}
	if len(patchOps) == 0 {
		return &admissionv1.AdmissionResponse{Allowed: true}, nil
	}
	patchBytes, err := json.Marshal(patchOps)
	if err != nil {
		return errorToAdmissionResponse(err), err
	}
	pt := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		Allowed:   true,
		Patch:     patchBytes,
		PatchType: &pt,
	}, nil
}

// respondWithAllowed writes a minimal AdmissionReview response that allows the
// request without any patches. Used for requests that should be silently ignored.
func respondWithAllowed(w http.ResponseWriter, ar admissionv1.AdmissionReview, gvk *schema.GroupVersionKind) {
	resp := admissionv1.AdmissionReview{
		Response: &admissionv1.AdmissionResponse{
			Allowed: true,
		},
	}
	if ar.Request != nil {
		resp.Response.UID = ar.Request.UID
	}
	if gvk != nil {
		resp.SetGroupVersionKind(*gvk)
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", runtime.ContentTypeJSON)
	if _, err = w.Write(respBytes); err != nil {
		log.Errorf("Failed to write response for non-Dapr pod: %v", err)
	}
}

// podHasDaprEnabled checks if the pod in the admission request has the
// "dapr.io/enabled" annotation set to "true". If the pod cannot be
// unmarshalled, it returns true to fall through to the normal processing path.
func podHasDaprEnabled(req *admissionv1.AdmissionRequest) bool {
	if req == nil || len(req.Object.Raw) == 0 {
		return true
	}
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		// If we can't unmarshal the pod, let the normal flow handle the error.
		return true
	}
	return strings.EqualFold(pod.Annotations[annotations.KeyEnabled], "true")
}

func (i *injector) allowServiceAccountUser(reviewRequestUserInfo string) (allowedUID bool) {
	if i.namespaceNameMatcher == nil {
		return false
	}

	namespacedName, prefixFound := strings.CutPrefix(reviewRequestUserInfo, serviceAccountUserInfoPrefix)
	if !prefixFound {
		return false
	}
	namespacedNameParts := strings.Split(namespacedName, ":")
	if len(namespacedNameParts) <= 1 {
		return false
	}
	return i.namespaceNameMatcher.MatchesNamespacedName(namespacedNameParts[0], namespacedNameParts[1])
}
