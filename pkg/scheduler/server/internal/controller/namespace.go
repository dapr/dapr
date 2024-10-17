/*
Copyright 2024 The Dapr Authors
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/cron"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/serialize"
)

type namespace struct {
	cron     cron.Interface
	nsReader client.Reader
}

func (n *namespace) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log.Debugf("Reconciling namespace %s", req.Name)

	cronClient, err := n.cron.Client(ctx)
	if err != nil {
		log.Errorf("Failed to get etcd cron client: %s", err)
		return ctrl.Result{}, err
	}

	var ns corev1.Namespace
	err = n.nsReader.Get(ctx, client.ObjectKey{Name: req.Name}, &ns)
	if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	log.Infof("Deleting jobs from deleted namespace %s", req.Name)
	err = cronClient.DeletePrefixes(ctx, serialize.PrefixesFromNamespace(req.Name)...)
	if err != nil {
		log.Errorf("Failed to delete cron jobs for namespace %s: %s", req.Name, err)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
