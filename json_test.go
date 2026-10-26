package qngg

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"reflect"
	"testing"

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

	assert.True(t, implementsJSONUnmarshaler(reflect.TypeFor[streamedText]()))

	t.Run("plain", func(t *testing.T) {
		v := must(ctx.Eval(context.Background(), "test.js", `({ text: "hello" })`))
		defer v.Free()

		got, err := toGo[target](v)
		require.NoError(t, err)
		assert.Equal(t, "hello", got.Text.S)
	})

	// JS strings may hold lone surrogates; JSON.stringify escapes them and
	// they must decode as U+FFFD instead of failing.
	t.Run("lone surrogate", func(t *testing.T) {
		v := must(ctx.Eval(context.Background(), "test.js", `({ text: "a\ud800b" })`))
		defer v.Free()

		got, err := toGo[target](v)
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
		v := must(ctx.Eval(context.Background(), "test.js", src))
		got, err := v.jsonStringify()
		v.Free()
		require.NoError(t, err, src)
		assert.Equal(t, want, got, src)
	}

	v := must(ctx.Eval(context.Background(), "test.js", `const o = {}; o.self = o; o`))
	defer v.Free()

	_, err := v.jsonStringify()
	assert.Error(t, err)
}
