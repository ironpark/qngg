package qjs_test

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"reflect"
	"testing"

	"github.com/fastschema/qjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// streamedText implements only the json/v2 streaming unmarshaler.
type streamedText struct {
	S string
}

func (t *streamedText) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	return json.UnmarshalDecode(dec, &t.S)
}

func TestJsonUnmarshalerFromField(t *testing.T) {
	_, ctx := setupTestContext(t)

	type target struct {
		Text streamedText `json:"text"`
	}

	assert.True(t, qjs.IsImplementsJSONUnmarshaler(reflect.TypeFor[streamedText]()))

	t.Run("plain", func(t *testing.T) {
		v := must(ctx.Eval("test.js", qjs.Code(`({ text: "hello" })`)))
		defer v.Free()

		got, err := qjs.ToGoValue[target](v)
		require.NoError(t, err)
		assert.Equal(t, "hello", got.Text.S)
	})

	// JS strings may hold lone surrogates; JSON.stringify escapes them and
	// they must decode as U+FFFD instead of failing.
	t.Run("lone surrogate", func(t *testing.T) {
		v := must(ctx.Eval("test.js", qjs.Code(`({ text: "a\ud800b" })`)))
		defer v.Free()

		got, err := qjs.ToGoValue[target](v)
		require.NoError(t, err)
		assert.Equal(t, "a�b", got.Text.S)
	})
}

func TestJSONStringifyPrimitives(t *testing.T) {
	_, ctx := setupTestContext(t)

	for src, want := range map[string]string{
		`"hi"`:          `"hi"`,
		`"hello"`:       `"hello"`,
		`[1,2]`:         `[1,2]`,
		`({a:"hello"})`: `{"a":"hello"}`,
		`42`:            `42`,
		`undefined`:     `undefined`,
	} {
		v := must(ctx.Eval("test.js", qjs.Code(src)))
		got, err := v.JSONStringify()
		v.Free()
		require.NoError(t, err, src)
		assert.Equal(t, want, got, src)
	}

	v := must(ctx.Eval("test.js", qjs.Code(`const o = {}; o.self = o; o`)))
	defer v.Free()

	_, err := v.JSONStringify()
	assert.Error(t, err)
}
