package errorx

// TypeModifier is a way to change a default behaviour for an error type, directly or via type hierarchy.
// Modification is intentionally one-way, as it provides much more clarity.
// If there is a modifier on a type or a namespace, all its descendants definitely have the same default behaviour.
// If some of a subtypes must lack a specific modifier, then the modifier must be removed from the common ancestor.
type TypeModifier int

const (
	// TypeModifierTransparent is a type modifier; an error type with such modifier creates transparent wrappers by default
	TypeModifierTransparent TypeModifier = 1
	// TypeModifierOmitStackTrace is a type modifier; an error type with such modifier omits the stack trace collection upon creation of an error instance
	TypeModifierOmitStackTrace TypeModifier = 2
)

type modifiers interface {
	CollectStackTrace() bool
	Transparent() bool
	ReplaceWith(new modifiers) modifiers
}

var _ modifiers = noModifiers{}
var _ modifiers = typeModifiers{}
var _ modifiers = inheritedModifiers{}

type noModifiers struct {
}

func (noModifiers) CollectStackTrace() bool {
	return true
}

func (noModifiers) Transparent() bool {
	return false
}

func (noModifiers) ReplaceWith(new modifiers) modifiers {
	return new
}

type typeModifiers struct {
	omitStackTrace bool
	transparent    bool
}

func newTypeModifiers(modifiers ...TypeModifier) modifiers {
	m := typeModifiers{}
	for _, modifier := range modifiers {
		switch modifier {
		case TypeModifierOmitStackTrace:
			m.omitStackTrace = true
		case TypeModifierTransparent:
			m.transparent = true
		}
	}
	return m
}

func (m typeModifiers) CollectStackTrace() bool {
	return !m.omitStackTrace
}

func (m typeModifiers) Transparent() bool {
	return m.transparent
}

func (typeModifiers) ReplaceWith(new modifiers) modifiers {
	panic("attempt to modify type modifiers the second time")
}

type inheritedModifiers struct {
	parent   modifiers
	override modifiers
}

func newInheritedModifiers(modifiers modifiers) modifiers {
	if _, ok := modifiers.(noModifiers); ok {
		return noModifiers{}
	}

	return inheritedModifiers{
		parent:   modifiers,
		override: noModifiers{},
	}
}

func (m inheritedModifiers) CollectStackTrace() bool {
	return m.parent.CollectStackTrace() && m.override.CollectStackTrace()
}

func (m inheritedModifiers) Transparent() bool {
	return m.parent.Transparent() || m.override.Transparent()
}

func (m inheritedModifiers) ReplaceWith(new modifiers) modifiers {
	m.override = new
	return m
}
