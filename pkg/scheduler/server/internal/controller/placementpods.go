/*
Copyright 2026 The Dapr Authors
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

package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type placementPods struct {
	ctrl      *Controller
	podReader client.Reader
	namespace string
}

// A Terminating pod still counts: presence means deployed, and erring
// toward withholding is the safe side.
func (p *placementPods) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log.Debugf("Reconciling placement pod %s", req.Name)

	var pods corev1.PodList
	if err := p.podReader.List(ctx, &pods,
		client.InNamespace(p.namespace),
		client.MatchingLabels(placementPodLabels),
	); err != nil {
		log.Errorf("Failed to list placement pods: %s", err)
		return ctrl.Result{}, err
	}

	p.ctrl.setPlacementPresence(len(pods.Items) > 0)
	return ctrl.Result{}, nil
}
