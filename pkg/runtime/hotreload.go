package runtime

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/cenkalti/backoff/v4"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

// watchComponentUpdates watches for component updates and updates the runtime.
func (a *DaprRuntime) watchComponentUpdates(ctx context.Context) error {
	if a.operatorClient == nil {
		return nil
	}

	parseAndUpdate := func(compRaw []byte) {
		var component componentsV1alpha1.Component
		if err := json.Unmarshal(compRaw, &component); err != nil {
			log.Errorf("error unmarshalling component: %s", err)
			return
		}

		if !a.isComponentAuthorized(component) {
			log.Debugf("Received unauthorized component update, ignored. name: %s, type: %s/%s", component.ObjectMeta.Name, component.Spec.Type, component.Spec.Version)
			return
		}

		log.Debugf("Received component update. name: %s, type: %s/%s", component.ObjectMeta.Name, component.Spec.Type, component.Spec.Version)
		updated, err := a.onComponentUpdated(component)
		if err != nil {
			log.Errorf("error updating component: %s", err)
			return
		}
		if updated {
			log.Info("Component updated successfully")
			return
		}
		log.Info("Component update skipped: .spec field unchanged")
	}

	needList := false
	for {
		var stream operatorv1pb.Operator_ComponentUpdateClient //nolint:nosnakecase

		// Retry on stream error.
		backoff.Retry(func() error {
			var err error
			stream, err = a.operatorClient.ComponentUpdate(context.Background(), &operatorv1pb.ComponentUpdateRequest{
				Namespace: a.namespace,
				PodName:   a.podName,
			})
			if err != nil {
				log.Errorf("error from operator stream: %s", err)
				return err
			}
			return nil
		}, backoff.NewExponentialBackOff())

		if needList {
			// We should get all components again to avoid missing any updates during
			// the failure time.
			backoff.Retry(func() error {
				resp, err := a.operatorClient.ListComponents(context.Background(), &operatorv1pb.ListComponentsRequest{
					Namespace: a.namespace,
				})
				if err != nil {
					log.Errorf("error listing components: %s", err)
					return err
				}

				comps := resp.GetComponents()
				for i := 0; i < len(comps); i++ {
					// avoid missing any updates during the init component time.
					go func(comp []byte) {
						parseAndUpdate(comp)
					}(comps[i])
				}

				return nil
			}, backoff.NewExponentialBackOff())
		}

		for {
			c, err := stream.Recv()
			if err != nil {
				// Retry on stream error.
				needList = true
				log.Errorf("error from operator stream: %s", err)
				break
			}

			parseAndUpdate(c.GetComponent())
		}
	}

	return nil
}

func (a *DaprRuntime) onComponentUpdated(component componentsV1alpha1.Component) (bool, error) {
	oldComp, exists := a.getComponent(component.Spec.Type, component.Name)
	newComp, _ := a.processComponentSecrets(component)

	if exists && reflect.DeepEqual(oldComp.Spec, newComp.Spec) {
		return false, nil
	}

	log.Info("Closing component with old spec")
	if err := closeComponent(oldComp, "component updated"); err != nil {
		return false, err
	}

	log.Info("Starting component with new spec")
	if err := a.processComponentAndDependents(newComp); err != nil {
		return false, err
	}

	return true, nil
}
