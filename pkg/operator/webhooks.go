package operator

import (
	"os"

	_ "k8s.io/client-go/plugin/pkg/client/auth" // Register the k8s client auth
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	subscriptionsapi_v1alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	subscriptionsapi_v2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
)

func RunWebhooks(enableLeaderElection bool) {
	conf, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("unable to get controller runtime configuration, err: %s", err)
	}

	mgr, err := ctrl.NewManager(conf, ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     "0",
		Port:                   19443,
		HealthProbeBindAddress: "0",
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "webhooks.dapr.io",
	})
	if err != nil {
		log.Fatal(err, "unable to start webhooks")
	}

	/*
		Make sure to set `ENABLE_WEBHOOKS=false` when we run locally.
	*/
	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		if err = ctrl.NewWebhookManagedBy(mgr).
			For(&subscriptionsapi_v1alpha1.Subscription{}).
			Complete(); err != nil {
			log.Fatalf("unable to create webhook Subscriptions v1alpha1: %v", err)
		}
		if err = ctrl.NewWebhookManagedBy(mgr).
			For(&subscriptionsapi_v2alpha1.Subscription{}).
			Complete(); err != nil {
			log.Fatalf("unable to create webhook Subscriptions v2alpha1: %v", err)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Fatalf("unable to set up health check: %v", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Fatalf("unable to set up ready check: %v", err)
	}

	log.Info("starting webhooks")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatalf("problem running webhooks: %v", err)
	}
}
