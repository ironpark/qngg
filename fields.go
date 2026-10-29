package qngg

import (
	"reflect"
	"strings"
	"sync"
)

// structField is a field of a struct type that converts to and from a
// property of a JavaScript object, possibly promoted from an embedded struct.
type structField struct {
	reflect.StructField

	name  string // property name: the json tag name or the field name
	index []int  // path from the outer struct, through exported embedded structs
}

// structFields lists the fields of a struct type in declaration order, with
// the fields of embedded structs in place of them.
type structFields struct {
	list   []structField
	byName map[string]int // index in list
}

var structFieldCache sync.Map // reflect.Type -> *structFields

// structFieldsOf returns the fields of the struct type t, following the
// rules of encoding/json: exported fields not tagged json:"-", with the
// fields of embedded structs promoted unless a shallower field, or an
// earlier one at the same depth, has the same name. Unlike encoding/json,
// unexported embedded structs are skipped, and embedded structs are
// promoted even if their json tag names them.
func structFieldsOf(t reflect.Type) *structFields {
	if fields, ok := structFieldCache.Load(t); ok {
		return fields.(*structFields) //nolint:forcetypeassert // only *structFields is stored
	}

	var candidates []structField

	collectFields(t, nil, map[reflect.Type]bool{t: true}, &candidates)

	fields := &structFields{byName: make(map[string]int)}
	depth := make(map[string]int)

	for _, f := range candidates {
		if d, seen := depth[f.name]; seen && d <= len(f.index) {
			continue
		}

		depth[f.name] = len(f.index)
	}

	for _, f := range candidates {
		if _, taken := fields.byName[f.name]; !taken && depth[f.name] == len(f.index) {
			fields.byName[f.name] = len(fields.list)
			fields.list = append(fields.list, f)
		}
	}

	actual, _ := structFieldCache.LoadOrStore(t, fields)

	return actual.(*structFields) //nolint:forcetypeassert // only *structFields is stored
}

// collectFields appends the fields of t, under the index path prefix, to
// fields. visited holds the embedded struct types on the path, to stop at
// types that embed themselves.
func collectFields(t reflect.Type, prefix []int, visited map[reflect.Type]bool, fields *[]structField) {
	for i := range t.NumField() {
		field := t.Field(i)

		name := jsonFieldName(field)
		if name == "" || !field.IsExported() {
			continue
		}

		index := append(append([]int{}, prefix...), i)

		if field.Anonymous {
			ft := field.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}

			if ft.Kind() == reflect.Struct {
				if visited[ft] {
					continue
				}

				visited[ft] = true
				collectFields(ft, index, visited, fields)
				delete(visited, ft)

				continue
			}
		}

		*fields = append(*fields, structField{StructField: field, name: name, index: index})
	}
}

// jsonFieldName returns the property name of field: its json tag name, or
// its name if the tag has none. It is empty for fields tagged json:"-".
func jsonFieldName(field reflect.StructField) string {
	tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")

	switch tag {
	case "-":
		return ""
	case "":
		return field.Name
	default:
		return tag
	}
}
