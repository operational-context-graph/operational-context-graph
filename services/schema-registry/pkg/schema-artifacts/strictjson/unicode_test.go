package strictjson

import (
	"fmt"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseRejectsInvalidUnicode checks escapes before decoding can replace invalid surrogate code units.
func TestParseRejectsInvalidUnicode(t *testing.T) {
	for _, input := range []string{
		`"\ud800"`, `"\udbff"`, `"\udc00"`, `"\udfff"`,
		`"\ud800a"`, `"\ud800\n"`, `"\ud800\u0061"`,
		`"\ud800\ud800"`, `"\udc00\ud800"`, `"\ud800\\udc00"`,
		`"\ud800\udc00\udfff"`, `"\\\ud800"`,
		`"\u"`, `"\u123"`, `"\u12xz"`, `"\ud800\u"`, `"\ud800\udc0"`,
		`"\ud800\uxxxx"`, `"\ud800\Udc00"`, `"\ud800`, `"\`,
	} {
		for _, document := range []string{input, `{"value":` + input + `}`, `{` + input + `:true}`, `[{"nested":[` + input + `]}]`} {
			t.Run(document, func(t *testing.T) {
				t.Parallel()
				data := []byte(document)
				value, err := Parse(data)
				require.ErrorIs(t, err, ErrMalformed)
				assert.Nil(t, value)
				assert.Equal(t, document, string(data), "parsing must not mutate input")
			})
		}
	}
}

// TestParseRejectsUnicodeNoncharacters covers every noncharacter in literal and escaped keys and values.
func TestParseRejectsUnicodeNoncharacters(t *testing.T) {
	noncharacters := make([]rune, 0, 66)
	for value := rune(0xfdd0); value <= 0xfdef; value++ {
		noncharacters = append(noncharacters, value)
	}
	for plane := rune(0); plane <= 16; plane++ {
		noncharacters = append(noncharacters, plane<<16|0xfffe, plane<<16|0xffff)
	}
	for _, value := range noncharacters {
		escaped := fmt.Sprintf(`"\u%04x"`, value)
		if value > 0xffff {
			high, low := utf16.EncodeRune(value)
			escaped = fmt.Sprintf(`"\u%04x\u%04x"`, high, low)
		}
		for _, input := range []string{`"` + string(value) + `"`, escaped} {
			for _, document := range []string{input, `{` + input + `:true}`} {
				t.Run(document, func(t *testing.T) {
					t.Parallel()
					parsed, err := Parse([]byte(document))
					require.ErrorIs(t, err, ErrUnicode)
					require.ErrorIs(t, err, ErrMalformed)
					assert.Nil(t, parsed)
				})
			}
		}
	}
}

// TestParsePreservesValidUnicode distinguishes valid pairs and replacement characters from malformed escapes.
func TestParsePreservesValidUnicode(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{`""`, ""},
		{`"\ud800\udc00"`, "\U00010000"},
		{`"\uDBFF\uDFFD"`, "\U0010fffd"},
		{`"\ud83d\ude00\ud83d\ude01"`, "😀😁"},
		{`"😀"`, "😀"},
		{`"\ud7ff\ue000"`, "\ud7ff\ue000"},
		{`"\ufdcf\ufdf0\ufffd"`, "\ufdcf\ufdf0\ufffd"},
		{`"�"`, "�"},
		{`"\u0000\n\t"`, "\x00\n\t"},
		{`"\\ud800"`, `\ud800`},
		{`"\u005cud800"`, `\ud800`},
		{`"\\\\udfff"`, `\\udfff`},
		{`"\"\\ud800\""`, `"\ud800"`},
	} {
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()
			value, err := Parse([]byte(test.input))
			require.NoError(t, err)
			assert.Equal(t, test.want, value)
			object, err := Object([]byte(`{` + test.input + `:` + test.input + `}`))
			require.NoError(t, err)
			assert.Equal(t, map[string]any{test.want: test.want}, object)
		})
	}
}
