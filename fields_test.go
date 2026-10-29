package qngg

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type FieldsInner struct {
	Shared string
	Inner  string `json:"inner"`
}

type FieldsOther struct {
	Shared string
	Other  string
}

type fieldsLabel string

type FieldsTagged struct {
	Tagged string
}

type FieldsSelf struct {
	*FieldsSelf

	Name string
}

type fieldsOuter struct {
	FieldsInner
	*FieldsOther
	fieldsLabel
	FieldsTagged `json:"tagged"`

	Name    string `json:"name,omitempty"`
	Skipped string `json:"-"`
	private string
}

func fieldNames(t reflect.Type) []string {
	var names []string
	for _, f := range structFieldsOf(t).list {
		names = append(names, f.name)
	}

	return names
}

func TestStructFieldsOf(t *testing.T) {
	t.Run("promotion and tags", func(t *testing.T) {
		// Shared is promoted from FieldsInner, the first at its depth.
		// Unexported embedded fields are skipped, and tagged embedded
		// structs are promoted.
		assert.Equal(t,
			[]string{"Shared", "inner", "Other", "Tagged", "name"},
			fieldNames(reflect.TypeFor[fieldsOuter]()),
		)

		field := structFieldsOf(reflect.TypeFor[fieldsOuter]()).byName["Shared"]
		assert.Equal(t, []int{0, 0}, field.index)
	})

	t.Run("shallower field wins", func(t *testing.T) {
		type outer struct {
			FieldsInner

			Shared int
		}

		field := structFieldsOf(reflect.TypeFor[outer]()).byName["Shared"]
		assert.Equal(t, []int{1}, field.index)
	})

	t.Run("self embedding terminates", func(t *testing.T) {
		assert.Equal(t, []string{"Name"}, fieldNames(reflect.TypeFor[FieldsSelf]()))
	})

	t.Run("concurrent use", func(t *testing.T) {
		type fresh struct {
			FieldsInner

			A, B int
		}

		var wg sync.WaitGroup
		for range 50 {
			wg.Go(func() {
				assert.Len(t, structFieldsOf(reflect.TypeFor[fresh]()).list, 4)
			})
		}
		wg.Wait()
	})
}

func TestStructFieldsRoundTrip(t *testing.T) {
	rt := must(New(Config{}))
	defer rt.Close()

	in := fieldsOuter{
		FieldsInner: FieldsInner{Shared: "s", Inner: "i"},
		Name:        "n",
		Skipped:     "x",
	}

	val := must(rt.ToValue(in))
	defer val.Free()

	keys := must(val.Keys())
	assert.Equal(t, []string{"Shared", "inner", "Tagged", "name"}, keys, "nil embedded pointer fields are skipped")

	out, err := val.As[fieldsOuter]()
	require.NoError(t, err)
	assert.Equal(t, fieldsOuter{FieldsInner: in.FieldsInner, Name: "n"}, out)

	obj := must(rt.Eval(context.Background(), "obj.js", `({Shared: "s", inner: "i", Other: "o", name: "n"})`))
	defer obj.Free()

	out, err = obj.As[fieldsOuter]()
	require.NoError(t, err)
	require.NotNil(t, out.FieldsOther, "embedded pointer is allocated for its fields")
	assert.Equal(t, "o", out.Other)
	assert.Empty(t, out.FieldsOther.Shared, "Shared goes to the first embedded struct")
}
