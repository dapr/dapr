package client

import (
	"log"
	"time"
)

type objEntry struct {
	metadata *V1ObjectMeta
	obj      interface{}
}

// ObjectLister is a function that knows how to list objects.
type ObjectLister func() ([]interface{}, string, error)

// ObjectWatcher is a function that knows how to perform a watch.
type ObjectWatcher func(resourceVersion string) (results <-chan *Result, errors <-chan error)

// EventHandler is implemented by objects that want event notifications
type EventHandler interface {
	OnAdd(obj interface{})
	OnUpdate(oldObj, newObj interface{})
	OnDelete(obj interface{})
}

// Informer is an interface for things that can provide notifications
type Informer interface {
	AddEventHandler(handler EventHandler)
}

// Lister is an interface for things that can list objects for all namespaces or by namespace
type Lister interface {
	List() []interface{}
	ByNamespace(namespace string) []interface{}
}

// Validate that we implement the interfaces
var _ Lister = &Cache{}
var _ Informer = &Cache{}

// Cache is an implementation of a List/Watch cache
type Cache struct {
	Extractor        func(interface{}) *V1ObjectMeta
	Lister           ObjectLister
	Watcher          ObjectWatcher
	allObjects       []objEntry
	namespaceObjects map[string][]objEntry
	eventHandlers    []EventHandler
}

func (c *Cache) AddEventHandler(handler EventHandler) {
	c.eventHandlers = append(c.eventHandlers, handler)
}

const maxSleep = 60 * time.Second

func (c *Cache) Run(stop <-chan bool) {
	sleep := 1 * time.Second
	for {
		select {
		case <-stop:
			return
		default:
			// pass
		}
		if err := c.ListWatch(); err != nil {
			log.Printf("%s\n", err.Error())
			time.Sleep(sleep)
			sleep = sleep * 2
			if sleep > maxSleep {
				sleep = maxSleep
			}
		} else {
			sleep = 1
		}
	}
}

func (c *Cache) ListWatch() error {
	objects, resourceVersion, err := c.Lister()
	if err != nil {
		return err
	}
	for ix := range objects {
		meta := c.Extractor(objects[ix])
		c.AddOrUpdate(meta, objects[ix])
	}
	results, errors := c.Watcher(resourceVersion)
	for {
		select {
		case result, ok := <-results:
			if !ok {
				return nil
			}
			c.ProcessResult(result)
		case err := <-errors:
			return err
		}
	}
}

func (c *Cache) ProcessResult(res *Result) {
	metadata := c.Extractor(res.Object)

	switch res.Type {
	case Added, Modified:
		c.AddOrUpdate(metadata, res.Object)
	case Deleted:
		c.Delete(metadata, res.Object)
	}
}

func (c *Cache) AddOrUpdate(metadata *V1ObjectMeta, obj interface{}) {
	var oldObj interface{}
	c.allObjects, oldObj = InsertOrUpdate(c.allObjects, metadata, obj)
	if len(metadata.Namespace) > 0 {
		c.namespaceObjects[metadata.Namespace], _ =
			InsertOrUpdate(c.namespaceObjects[metadata.Namespace], metadata, obj)
	}
	for ix := range c.eventHandlers {
		if oldObj == nil {
			c.eventHandlers[ix].OnAdd(obj)
		} else {
			c.eventHandlers[ix].OnUpdate(oldObj, obj)
		}
	}
}

func (c *Cache) Delete(metadata *V1ObjectMeta, obj interface{}) {
	var deleted bool
	c.allObjects, deleted = Delete(c.allObjects, metadata)
	if len(metadata.Namespace) > 0 {
		c.namespaceObjects[metadata.Namespace], _ =
			Delete(c.namespaceObjects[metadata.Namespace], metadata)
	}
	if deleted {
		for ix := range c.eventHandlers {
			c.eventHandlers[ix].OnDelete(obj)
		}
	}
}

func (c *Cache) List() []interface{} {
	result := make([]interface{}, len(c.allObjects))
	for ix := range c.allObjects {
		result[ix] = c.allObjects[ix].obj
	}
	return result
}

func (c *Cache) ByNamespace(namespace string) []interface{} {
	list := c.namespaceObjects[namespace]
	result := make([]interface{}, len(list))
	for ix := range list {
		result[ix] = list[ix].obj
	}
	return result
}

func InsertOrUpdate(list []objEntry, metadata *V1ObjectMeta, obj interface{}) ([]objEntry, interface{}) {
	ix := FindObject(list, metadata)
	if ix == -1 {
		return append(list, objEntry{metadata: metadata, obj: obj}), nil
	}
	oldObj := list[ix]
	list[ix] = objEntry{metadata: metadata, obj: obj}
	return list, oldObj
}

func Delete(list []objEntry, metadata *V1ObjectMeta) ([]objEntry, bool) {
	ix := FindObject(list, metadata)
	if ix == -1 {
		return list, false
	}
	return append(list[:ix], list[ix+1:]...), true
}

func FindObject(list []objEntry, metadata *V1ObjectMeta) int {
	for ix := range list {
		entry := &list[ix]
		if entry.metadata.Namespace == metadata.Namespace &&
			entry.metadata.Name == metadata.Name {
			return ix
		}
	}
	return -1
}
