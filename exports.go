package qngg

import (
	"fmt"

	wasmtime "github.com/ironpark/wasmtime-pure-go"
)

// export identifies a function exported by the QuickJS module. Exports are
// looked up on first use and cached in Runtime.funcs.
type export uint8

const (
	xMalloc export = iota
	xFree
	xStdAwait

	xNewQJS
	xQJSFree
	xGetContext
	xFreeValue
	xCloneValue

	xGetGlobalObject
	xGetException
	xHasException
	xThrow

	xFreeAtom
	xAtomToValue
	xFreeCString
	xGetProperty
	xToBool

	xEval
	xLoad
	xCompile

	xToStringBuf
	xAtomToStringBuf
	xJSONStringifyBuf
	xGetArrayBuffer
	xGetOwnPropertyAtoms
	xFreeAtoms
	xJSFree

	xGetPropertyLen
	xSetPropertyLen
	xHasPropertyLen
	xDeletePropertyLen
	xGetPropertyValue
	xSetPropertyValue
	xGetPropertyUint32
	xSetPropertyUint32

	xToInt64
	xToInt32
	xToUint32
	xToFloat64
	xToEpochTime

	xNewStringLen
	xNewObject
	xNewArray
	xNewDate
	xNewError
	xNewBigInt64
	xNewBigUint64
	xNewBigIntDecimal
	xNewArrayBufferCopy
	xParseJSON

	xCall
	xCallConstructor
	xNewGlobal

	xNewGoFunction
	xNewProxyValue
	xGetProxyID
	xGetKind

	numExports
)

var exportNames = [numExports]string{
	xMalloc:   "malloc",
	xFree:     "free",
	xStdAwait: "js_std_await",

	xNewQJS:     "New_QJS",
	xQJSFree:    "QJS_Free",
	xGetContext: "QJS_GetContext",
	xFreeValue:  "QJS_FreeValue",
	xCloneValue: "QJS_CloneValue",

	xGetGlobalObject: "JS_GetGlobalObject",
	xGetException:    "JS_GetException",
	xHasException:    "JS_HasException",
	xThrow:           "JS_Throw",

	xFreeAtom:    "JS_FreeAtom",
	xAtomToValue: "JS_AtomToValue",
	xFreeCString: "JS_FreeCString",
	xGetProperty: "JS_GetProperty",
	xToBool:      "JS_ToBool",

	xEval:    "QJS_Eval",
	xLoad:    "QJS_Load",
	xCompile: "QJS_Compile",

	xToStringBuf:         "QJS_ToStringBuf",
	xAtomToStringBuf:     "QJS_AtomToStringBuf",
	xJSONStringifyBuf:    "QJS_JSONStringifyBuf",
	xGetArrayBuffer:      "QJS_GetArrayBuffer",
	xGetOwnPropertyAtoms: "QJS_GetOwnPropertyAtoms",
	xFreeAtoms:           "QJS_FreeAtoms",
	xJSFree:              "js_free",

	xGetPropertyLen:    "QJS_GetPropertyLen",
	xSetPropertyLen:    "QJS_SetPropertyLen",
	xHasPropertyLen:    "QJS_HasPropertyLen",
	xDeletePropertyLen: "QJS_DeletePropertyLen",
	xGetPropertyValue:  "QJS_GetPropertyValue",
	xSetPropertyValue:  "QJS_SetPropertyValue",
	xGetPropertyUint32: "JS_GetPropertyUint32",
	xSetPropertyUint32: "QJS_SetPropertyUint32",

	xToInt64:     "QJS_ToInt64",
	xToInt32:     "QJS_ToInt32",
	xToUint32:    "QJS_ToUint32",
	xToFloat64:   "QJS_ToFloat64",
	xToEpochTime: "QJS_ToEpochTime",

	xNewStringLen:       "JS_NewStringLen",
	xNewObject:          "JS_NewObject",
	xNewArray:           "JS_NewArray",
	xNewDate:            "JS_NewDate",
	xNewError:           "JS_NewError",
	xNewBigInt64:        "JS_NewBigInt64",
	xNewBigUint64:       "JS_NewBigUint64",
	xNewBigIntDecimal:   "QJS_NewBigIntDecimal",
	xNewArrayBufferCopy: "JS_NewArrayBufferCopy",
	xParseJSON:          "QJS_ParseJSON",

	xCall:            "JS_Call",
	xCallConstructor: "JS_CallConstructor",
	xNewGlobal:       "QJS_NewGlobal",

	xNewGoFunction: "QJS_NewGoFunction",
	xNewProxyValue: "QJS_NewProxyValue",
	xGetProxyID:    "QJS_GetProxyID",
	xGetKind:       "QJS_GetKind",
}

func (x export) String() string { return exportNames[x] }

// wasmFunc is a resolved export of the QuickJS module.
type wasmFunc struct {
	fn *wasmtime.Func
	// size of the args-and-results buffer: max(params, results)
	slots     int
	hasResult bool
	// whether the single result is a 32-bit value that must be zero-extended
	result32 bool
}

// export returns the export x, looking it up on first use.
func (r *Runtime) export(x export) *wasmFunc {
	if f := r.funcs[x]; f != nil {
		return f
	}

	f := r.lookup(x.String())
	r.funcs[x] = f

	return f
}

// lookup resolves the export `name`. It panics if there is none.
func (r *Runtime) lookup(name string) *wasmFunc {
	fn := r.instance.GetFunc(r.store, name)
	if fn == nil {
		panic(fmt.Errorf("WASM function %s not found", name))
	}

	ty := fn.Type(r.store)
	params, results := ty.Params(), ty.Results()
	f := &wasmFunc{
		fn:        fn,
		slots:     max(len(params), len(results)),
		hasResult: len(results) > 0,
	}

	if len(results) == 1 {
		kind := results[0].Kind()
		f.result32 = kind == wasmtime.KindI32 || kind == wasmtime.KindF32
	}

	return f
}
