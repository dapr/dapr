package errorx

import "fmt"

// Namespace is a way go group a number of error types together, and each error type belongs to exactly one namespace.
// Namespaces may form hierarchy, with child namespaces inheriting the traits and modifiers of a parent.
// Those modifiers and traits are then passed upon all error types in the namespace.
// In formatting, a dot notation is used, for example:
//
// 		namespace.sub_namespace.type.subtype
//
type Namespace struct {
	parent    *Namespace
	id        uint64
	name      string
	traits    []Trait
	modifiers modifiers
}

// NamespaceKey is a comparable descriptor of a Namespace.
type NamespaceKey struct {
	id uint64
}

// NewNamespace defines a namespace with a name and, optionally, a number of inheritable traits.
func NewNamespace(name string, traits ...Trait) Namespace {
	namespace := newNamespace(nil, name, traits...)
	globalRegistry.registerNamespace(namespace)
	return namespace
}

// NewSubNamespace defines a child namespace that inherits all that is defined for a parent and, optionally, adds some more.
func (n Namespace) NewSubNamespace(name string, traits ...Trait) Namespace {
	namespace := newNamespace(&n, name, traits...)
	globalRegistry.registerNamespace(namespace)
	return namespace
}

// ApplyModifiers makes a one-time modification of defaults in error creation.
func (n Namespace) ApplyModifiers(modifiers ...TypeModifier) Namespace {
	n.modifiers = n.modifiers.ReplaceWith(newTypeModifiers(modifiers...))
	return n
}

// NewType creates a new type within a namespace  that inherits all that is defined for namespace and, optionally, adds some more.
func (n Namespace) NewType(typeName string, traits ...Trait) *Type {
	return NewType(n, typeName, traits...)
}

// Key returns a comparison key for namespace.
func (n Namespace) Key() NamespaceKey {
	return NamespaceKey{
		id: n.id,
	}
}

// IsNamespaceOf checks whether or not an error belongs either to this namespace or some of its sub-namespaces.
func (n Namespace) IsNamespaceOf(t *Type) bool {
	namespace := t.namespace
	other := &namespace

	for other != nil {
		if n.Key() == other.Key() {
			return true
		}

		other = other.parent
	}

	return false
}

// FullName returns a full name of a namespace.
func (n Namespace) FullName() string {
	return n.name
}

func (n Namespace) String() string {
	return n.name
}

// Parent returns the immediate parent namespace, if present.
// The use of this function outside of a system layer that handles error types (see TypeSubscriber) is a code smell.
func (n Namespace) Parent() *Namespace {
	return n.parent
}

func (n Namespace) collectTraits() map[Trait]bool {
	result := make(map[Trait]bool)
	namespace := &n
	for namespace != nil {
		for _, trait := range namespace.traits {
			result[trait] = true
		}

		namespace = namespace.parent
	}

	return result
}

func newNamespace(parent *Namespace, name string, traits ...Trait) Namespace {
	createName := func() string {
		if parent == nil {
			return name
		}
		return fmt.Sprintf("%s.%s", parent.FullName(), name)
	}

	createModifiers := func() modifiers {
		if parent == nil {
			return noModifiers{}
		}
		return newInheritedModifiers(parent.modifiers)
	}

	namespace := Namespace{
		id:        nextInternalID(),
		parent:    parent,
		name:      createName(),
		traits:    append([]Trait(nil), traits...),
		modifiers: createModifiers(),
	}

	return namespace
}
