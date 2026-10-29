package qngg

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"reflect"
	"strings"
)

func isImplementError(rtype reflect.Type) bool {
	return rtype.Implements(reflect.TypeFor[error]())
}

// implementsJSONUnmarshaler checks if a type implements json.Unmarshaler
// or json.UnmarshalerFrom.
func implementsJSONUnmarshaler(t reflect.Type) bool {
	implements := func(t reflect.Type) bool {
		return t.Implements(reflect.TypeFor[json.Unmarshaler]()) ||
			t.Implements(reflect.TypeFor[json.UnmarshalerFrom]())
	}

	return implements(t) || implements(reflect.PointerTo(t))
}

// unmarshalJSON decodes the output of JS JSON.stringify into out. Strings in
// JS may hold lone surrogates, which JSON.stringify escapes, so invalid UTF-8
// is accepted (and replaced) rather than rejected.
func unmarshalJSON(s string, out any) error {
	return json.Unmarshal([]byte(s), out, jsontext.AllowInvalidUTF8(true)) //nolint:wrapcheck // wrapped by callers
}

// goTypeName creates a descriptive string for complex types.
func goTypeName(input any) string {
	var t reflect.Type

	switch v := input.(type) {
	case reflect.Type:
		t = v
	default:
		t = reflect.TypeOf(v)
	}

	switch t.Kind() {
	case reflect.Pointer:
		return "*" + goTypeName(t.Elem())
	case reflect.Slice:
		return "[]" + goTypeName(t.Elem())
	case reflect.Array:
		return fmt.Sprintf("[%d]%s", t.Len(), goTypeName(t.Elem()))
	case reflect.Map:
		return fmt.Sprintf("map[%s]%s", goTypeName(t.Key()), goTypeName(t.Elem()))
	case reflect.Chan:
		return "chan " + goTypeName(t.Elem())
	case reflect.Func:
		return goFuncSignature(t)
	default:
		return t.String()
	}
}

// goFuncSignature creates a readable string for function types.
func goFuncSignature(fnType reflect.Type) string {
	parts := []string{"func("}
	params := []string{}

	for i := range fnType.NumIn() {
		paramType := fnType.In(i)
		if fnType.IsVariadic() && i == fnType.NumIn()-1 {
			params = append(params, "..."+goTypeName(paramType.Elem()))
		} else {
			params = append(params, goTypeName(paramType))
		}
	}

	parts = append(parts, strings.Join(params, ", "))
	parts = append(parts, ")")

	if fnType.NumOut() > 0 {
		parts = append(parts, " ")
		if fnType.NumOut() > 1 {
			parts = append(parts, "(")
		}

		returns := []string{}
		for out := range fnType.Outs() {
			returns = append(returns, goTypeName(out))
		}

		parts = append(parts, strings.Join(returns, ", "))
		if fnType.NumOut() > 1 {
			parts = append(parts, ")")
		}
	}

	return strings.Join(parts, "")
}

// isConvertibleToJs checks if a Go type can be converted to a JavaScript type.
func isConvertibleToJs(rType reflect.Type, visited map[reflect.Type]bool, detail string) (err error) {
	// Prevent infinite recursion for recursive types
	if visited[rType] {
		return nil
	}

	visited[rType] = true
	if rType.Kind() == reflect.Pointer {
		return isConvertibleToJs(rType.Elem(), visited, detail)
	}

	switch rType.Kind() {
	case reflect.Chan:
		return nil
	case reflect.UnsafePointer:
		return newGoToJsErr("unsafe.Pointer", nil, detail)
	case reflect.Func:
		return nil
	case reflect.Slice:
		err := isConvertibleToJs(rType.Elem(), visited, detail)
		if err != nil {
			return newGoToJsErr("slice: "+goTypeName(rType.Elem()), nil, detail)
		}

		return nil
	case reflect.Array:
		err := isConvertibleToJs(rType.Elem(), visited, detail)
		if err != nil {
			return newGoToJsErr("array: "+goTypeName(rType.Elem()), nil, detail)
		}

		return nil
	case reflect.Map:
		keyType := rType.Key()

		err := isConvertibleToJs(keyType, visited, detail)
		if err != nil {
			return newGoToJsErr("map key: "+goTypeName(keyType), nil, detail)
		}

		valueType := rType.Elem()
		if err := isConvertibleToJs(valueType, visited, detail); err != nil {
			return newGoToJsErr("map value: "+goTypeName(valueType), nil, detail)
		}

		return nil
	case reflect.Struct:
		for _, field := range structFieldsOf(rType).list {
			if err := isConvertibleToJs(field.Type, visited, detail); err != nil {
				return newGoToJsErr(goTypeName(rType)+"."+field.Name, err)
			}
		}

		return nil
	case reflect.Interface:
		return nil
	}

	return nil
}

// isNumericType checks if a reflect.Type represents a numeric type.
func isNumericType(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128:
		return true
	default:
		return false
	}
}
