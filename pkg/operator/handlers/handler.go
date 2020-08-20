package handlers

import ctrl "sigs.k8s.io/controller-runtime"

// Handler is the interface for dealing with Dapr CRDs state changes
type Handler interface {
	Init() error
	Reconcile(req ctrl.Request) (ctrl.Result, error)
}
