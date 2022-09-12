package runtime

import (
	"os"
	"path/filepath"

	"github.com/dapr/dapr/pkg/components"
	"github.com/fsnotify/fsnotify"

    componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

func (a *DaprRuntime) watchPathForDynamicLoading() {
	//Check for any existing component files in directory
	dir, err := os.Open(a.runtimeConfig.Standalone.DynamicComponentsPath)
	if err != nil {
		log.Fatalf("failed to open dynamic components directory: %s", err)
	}
	for {
		files, err := dir.ReadDir(1)
		if err != nil {
			break
		}
		for _, file := range files {
			err := a.loadDynamicComponents(file.Name())
			if err != nil {
				log.Errorf("failed to load components from file : %s", file.Name())
			}
		}
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				log.Debug("file event:", event)
				if event.Op == fsnotify.Create || event.Op == fsnotify.Write {
					err := a.loadDynamicComponents(filepath.Base(event.Name))
					if err != nil {
						log.Errorf("failed to load components from file : %s", event.Name)
					}
				} else if event.Op == fsnotify.Remove || event.Op == fsnotify.Rename {
					err := a.unloadDynamicComponents(filepath.Base(event.Name))
                    if err != nil {
                        log.Errorf("failed to unload components from file : %s", event.Name)
                    }
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Error(err)
			}
		}
	}()
	err = watcher.Add(a.runtimeConfig.Standalone.DynamicComponentsPath)
	if err != nil {
		log.Error(err)
	}
	<-make(chan struct{})
}

func (a *DaprRuntime) loadDynamicComponents(filename string) error {
	loader := components.NewStandaloneComponents(a.runtimeConfig.Standalone)
	log.Info("loading dynamic components...")
	fileComps := loader.LoadDynamicComponentsFromFile(filename)
    componentsToLoad := []componentsV1alpha1.Component{}

	for _, comp := range fileComps {
		log.Infof("found component. name: %s, type: %s/%s", comp.ObjectMeta.Name, comp.Spec.Type, comp.Spec.Version)

        if a.IsComponentLoaded(comp) {
			log.Infof("component already loaded, skipping. name: %s, type: %s/%s", comp.ObjectMeta.Name, comp.Spec.Type, comp.Spec.Version)
			continue
		}
        componentsToLoad = append(componentsToLoad, comp)
	}

    a.dynamicComponents[DynamicComponentsFile(filename)] = componentsToLoad

	authorizedComps := a.getAuthorizedComponents(componentsToLoad)
	a.componentsLock.Lock()
	a.components = append(a.components, authorizedComps...)
	a.componentsLock.Unlock()

	for _, comp := range authorizedComps {
		a.pendingComponents <- comp
	}

	return nil
}

func (a *DaprRuntime) unloadDynamicComponents(filename string) error {
    comps := a.dynamicComponents[DynamicComponentsFile(filename)]
    if comps == nil {
        return nil
    }

    for _, comp := range comps {
        a.unloadComponent(comp)
        log.Infof("unloaded component. name: %s, type: %s/%s", comp.ObjectMeta.Name, comp.Spec.Type, comp.Spec.Version)
    }

    delete(a.dynamicComponents, DynamicComponentsFile(filename))

    return nil
}
