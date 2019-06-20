package errorx

import "sync"

// TypeSubscriber is an interface to receive callbacks on the registered error namespaces and types.
// This may be used to create a user-defined registry, for example, to check if all type names are unique.
// ISSUE: if .ApplyModifiers is called for a type/namespace, callback still receives a value without those modifiers.
type TypeSubscriber interface {
	// OnNamespaceCreated is called exactly once for each namespace
	OnNamespaceCreated(namespace Namespace)
	// OnTypeCreated is called exactly once for each type
	OnTypeCreated(t *Type)
}

// RegisterTypeSubscriber adds a new TypeSubscriber.
// A subscriber is guaranteed to receive callbacks for all namespaces and types.
// If a type is already registered at the moment of subscription, a callback for this type is called immediately.
func RegisterTypeSubscriber(s TypeSubscriber) {
	globalRegistry.registerTypeSubscriber(s)
}

type registry struct {
	mu              sync.Mutex
	subscribers     []TypeSubscriber
	knownNamespaces []Namespace
	knownTypes      []*Type
}

var globalRegistry = &registry{}

func (r *registry) registerNamespace(namespace Namespace) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.knownNamespaces = append(r.knownNamespaces, namespace)
	for _, s := range r.subscribers {
		s.OnNamespaceCreated(namespace)
	}
}

func (r *registry) registerType(t *Type) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.knownTypes = append(r.knownTypes, t)
	for _, s := range r.subscribers {
		s.OnTypeCreated(t)
	}
}

func (r *registry) registerTypeSubscriber(s TypeSubscriber) {
	for _, ns := range r.knownNamespaces {
		s.OnNamespaceCreated(ns)
	}

	for _, t := range r.knownTypes {
		s.OnTypeCreated(t)
	}

	r.subscribers = append(r.subscribers, s)
}
