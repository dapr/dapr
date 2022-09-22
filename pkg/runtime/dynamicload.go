package runtime

import (
	"path/filepath"

	"github.com/dapr/dapr/pkg/components"
	"github.com/fsnotify/fsnotify"

	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

func (a *DaprRuntime) watchPathForDynamicLoading() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Errorf("unable to create watcher for dynamic components, dynamic loading will not be supported: %s", err)
	}
	defer watcher.Close()

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				log.Debug("observed a file event in components directory:", event)
				if event.Op == fsnotify.Create || event.Op == fsnotify.Write {
					err := a.loadDynamicComponents(event.Name)
					if err != nil {
						log.Errorf("failed to load components from file: %s err: %s", event.Name, err)
					}
				} else if event.Op == fsnotify.Remove || event.Op == fsnotify.Rename {
					for _, comp := range a.dynamicComponents[DynamicComponentsManifest(event.Name)] {
						log.Warnf("file: %s deleted, component: %s will not be loaded on sidecar restart.", filepath.Base(event.Name), comp.Name)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Errorf("error while watching components directory for file events: %s", err)
			}
		}
	}()
	err = watcher.Add(a.runtimeConfig.Standalone.ComponentsPath)
	if err != nil {
		log.Error(err)
	}
	<-make(chan struct{})
}

func (a *DaprRuntime) loadDynamicComponents(manifestPath string) error {
	loader := components.NewStandaloneComponents(a.runtimeConfig.Standalone)
	log.Info("found new file in components directory, loading components from file: %s", manifestPath)
	fileComps := loader.LoadComponentsFromFile(manifestPath)
	componentsToLoad := []componentsV1alpha1.Component{}

	for _, comp := range fileComps {
		if a.IsComponentLoaded(comp) {
			log.Infof("component is already loaded, skipping. name: %s, type: %s/%s", comp.ObjectMeta.Name, comp.Spec.Type, comp.Spec.Version)
			continue
		}
		log.Infof("found component. name: %s, type: %s/%s", comp.ObjectMeta.Name, comp.Spec.Type, comp.Spec.Version)
		componentsToLoad = append(componentsToLoad, comp)
	}

	a.dynamicComponents[DynamicComponentsManifest(manifestPath)] = componentsToLoad

	authorizedComps := a.getAuthorizedComponents(componentsToLoad)
	a.componentsLock.Lock()
	a.components = append(a.components, authorizedComps...)
	a.componentsLock.Unlock()

	for _, comp := range authorizedComps {
		err := a.processComponentAndDependents(comp)
		if err != nil {
			if !comp.Spec.IgnoreErrors {
				log.Warnf("error processing component %s, error: %s", comp.Name, err.Error())
			}
			log.Errorf(err.Error())
		}
		err = a.initDynamicComponent(comp)
		if err != nil {
			log.Errorf("error initializing component %s, error: %s", comp.Name, err.Error())
		}
	}
	return nil
}

func (a *DaprRuntime) initDynamicComponent(comp componentsV1alpha1.Component) error {
	cat := a.extractComponentCategory(comp)
	if cat == pubsubComponent {
		a.beginPubSub(comp.Name)
	} else if cat == bindingsComponent {
		if a.bindingsRegistry.HasInputBinding(comp.Spec.Type, comp.Spec.Version) {
			a.readFromBinding(a.inputBindingsCtx, comp.Name, a.inputBindings[comp.Name])
		}
	}
	// No separate initialization is required for other component categories.
	return nil
}
