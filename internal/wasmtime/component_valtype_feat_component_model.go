package wasmtime

import "runtime"

// ComponentValTypeKind discriminates the WIT type that a [ComponentValType]
// represents. Only constants for the 13 primitive kinds (bool through
// string) are exposed in this release; constants for the composite kinds
// (list / record / tuple / variant / enum / option / result / flags / own /
// borrow / future / stream / error-context / map) are intentionally
// commented out below — they will be uncommented when each composite kind
// gets a dedicated payload accessor and a test path that exercises it.
type ComponentValTypeKind uint8

const (
	ComponentValTypeKindBool   ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_BOOL
	ComponentValTypeKindS8     ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_S8
	ComponentValTypeKindS16    ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_S16
	ComponentValTypeKindS32    ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_S32
	ComponentValTypeKindS64    ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_S64
	ComponentValTypeKindU8     ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_U8
	ComponentValTypeKindU16    ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_U16
	ComponentValTypeKindU32    ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_U32
	ComponentValTypeKindU64    ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_U64
	ComponentValTypeKindF32    ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_F32
	ComponentValTypeKindF64    ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_F64
	ComponentValTypeKindChar   ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_CHAR
	ComponentValTypeKindString ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_STRING

	// Composite-kind constants are deferred until each gets a payload
	// accessor that returns the corresponding sub-type wrapper, with a
	// test path. Uncomment as each one lands.
	//
	// ComponentValTypeKindList         ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_LIST
	// ComponentValTypeKindRecord       ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_RECORD
	// ComponentValTypeKindTuple        ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_TUPLE
	// ComponentValTypeKindVariant      ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_VARIANT
	// ComponentValTypeKindEnum         ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_ENUM
	// ComponentValTypeKindOption       ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_OPTION
	// ComponentValTypeKindResult       ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_RESULT
	// ComponentValTypeKindFlags        ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_FLAGS
	// ComponentValTypeKindOwn          ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_OWN
	// ComponentValTypeKindBorrow       ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_BORROW
	// ComponentValTypeKindFuture       ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_FUTURE
	// ComponentValTypeKindStream       ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_STREAM
	// ComponentValTypeKindErrorContext ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_ERROR_CONTEXT
	// ComponentValTypeKindMap          ComponentValTypeKind = WASMTIME_COMPONENT_VALTYPE_MAP
)

// ComponentValType describes the WIT type of a value in the component
// model.
//
// In this release [ComponentValType.Kind] returns one of the 13 primitive
// [ComponentValTypeKind] constants. If the underlying value type is a
// composite kind, the returned uint8 will not match any exposed constant
// — those, along with payload accessors that return their sub-type
// wrappers, arrive in follow-up work.
type ComponentValType struct {
	val    wasmtime_component_valtype_t
	closed bool
}

func mkComponentValType(val wasmtime_component_valtype_t) *ComponentValType {
	vt := &ComponentValType{val: val}
	runtime.SetFinalizer(vt, func(vt *ComponentValType) {
		vt.Close()
	})
	return vt
}

// Kind returns the discriminator of this value type.
func (vt *ComponentValType) Kind() ComponentValTypeKind {
	if vt.closed {
		panic("object has been closed already")
	}
	maybeGC()
	return ComponentValTypeKind(go_component_valtype_kind(&vt.val))
}

// Close deallocates this value type explicitly.
func (vt *ComponentValType) Close() {
	if vt.closed {
		return
	}
	runtime.SetFinalizer(vt, nil)
	wasmtime_component_valtype_delete(&vt.val)
	vt.closed = true
}
