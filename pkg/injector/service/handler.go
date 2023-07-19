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
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/dapr/dapr/utils"
)

func (i *injector) handleRequest(w http.ResponseWriter, r *http.Request) {
	RecordSidecarInjectionRequestsCount()

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
		log.Error("Empty body")
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

	var patchOps jsonpatch.Patch
	patchedSuccessfully := false

	ar := admissionv1.AdmissionReview{}
	_, gvk, err := i.deserializer.Decode(body, nil, &ar)
	if err != nil {
		log.Errorf("Can't decode body: %v", err)
	} else {
		allowServiceAccountUser := i.allowServiceAccountUser(ar.Request.UserInfo.Username)

		if !(allowServiceAccountUser || utils.Contains(i.authUIDs, ar.Request.UserInfo.UID) || utils.Contains(ar.Request.UserInfo.Groups, systemGroup)) {
			log.Errorf("service account '%s' not on the list of allowed controller accounts", ar.Request.UserInfo.Username)
		} else if ar.Request.Kind.Kind != "Pod" {
			log.Errorf("invalid kind for review: %s", ar.Kind)
		} else {
			patchOps, err = i.getPodPatchOperations(r.Context(), &ar)
			if err == nil {
				patchedSuccessfully = true
			}
		}
	}

	diagAppID := getAppIDFromRequest(ar.Request)

	var admissionResponse *admissionv1.AdmissionResponse
	if err != nil {
		admissionResponse = errorToAdmissionResponse(err)
		log.Errorf("Sidecar injector failed to inject for app '%s'. Error: %s", diagAppID, err)
		RecordFailedSidecarInjectionCount(diagAppID, "patch")
	} else if len(patchOps) == 0 {
		admissionResponse = &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	} else {
		var patchBytes []byte
		patchBytes, err = json.Marshal(patchOps)
		if err != nil {
			admissionResponse = errorToAdmissionResponse(err)
		} else {
			admissionResponse = &admissionv1.AdmissionResponse{
				Allowed: true,
				Patch:   patchBytes,
				PatchType: func() *admissionv1.PatchType {
					pt := admissionv1.PatchTypeJSONPatch
					return &pt
				}(),
			}
		}
	}

	admissionReview := admissionv1.AdmissionReview{
		Response: admissionResponse,
	}
	if admissionResponse != nil && ar.Request != nil {
		admissionReview.Response.UID = ar.Request.UID
		admissionReview.SetGroupVersionKind(*gvk)
	}

	respBytes, err := json.Marshal(admissionReview)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Errorf("Sidecar injector failed to inject for app '%s'. Can't serialize response: %s", diagAppID, err)
		RecordFailedSidecarInjectionCount(diagAppID, "response")
		return
	}
	w.Header().Set("Content-Type", runtime.ContentTypeJSON)
	_, err = w.Write(respBytes)
	if err != nil {
		log.Errorf("Sidecar injector failed to inject for app '%s'. Failed to write response: %v", diagAppID, err)
		RecordFailedSidecarInjectionCount(diagAppID, "write_response")
		return
	}

	if patchedSuccessfully {
		log.Infof("Sidecar injector succeeded injection for app '%s'", diagAppID)
		RecordSuccessfulSidecarInjectionCount(diagAppID)
	} else {
		log.Errorf("Admission succeeded, but pod was not patched. No sidecar injected for '%s'", diagAppID)
		RecordFailedSidecarInjectionCount(diagAppID, "pod_patch")
	}
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
