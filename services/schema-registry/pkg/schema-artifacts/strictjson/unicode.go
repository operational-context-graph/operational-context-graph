package strictjson

import (
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// validUnicode rejects unpaired surrogates and Unicode noncharacters before encoding/json can replace invalid escapes.
// Input MUST already contain valid UTF-8 and satisfy the document byte limit.
// The JSON decoder remains responsible for syntax; backslashes in valid JSON occur only within strings.
func validUnicode(input []byte) bool {
	for index := 0; index < len(input); {
		var value rune
		switch {
		case input[index] == '\\':
			if index+1 == len(input) {
				return false
			}
			if input[index+1] != 'u' {
				// Skip the whole escape, including escaped backslashes and quotes.
				index += 2
				continue
			}
			var ok bool
			value, ok = unicodeEscape(input[index+2:])
			if !ok {
				return false
			}
			index += 6
			if value >= 0xd800 && value <= 0xdbff {
				if len(input)-index < 6 || input[index] != '\\' || input[index+1] != 'u' {
					return false
				}
				low, ok := unicodeEscape(input[index+2:])
				if !ok || low < 0xdc00 || low > 0xdfff {
					return false
				}
				value = utf16.DecodeRune(value, low)
				index += 6
			} else if utf16.IsSurrogate(value) {
				return false
			}
		case input[index] >= utf8.RuneSelf:
			var size int
			value, size = utf8.DecodeRune(input[index:])
			index += size
		default:
			index++
			continue
		}
		if value >= 0xfdd0 && value <= 0xfdef || value&0xffff >= 0xfffe {
			return false
		}
	}
	return true
}

// unicodeEscape reads the four hexadecimal digits after a JSON Unicode escape introducer.
func unicodeEscape(input []byte) (rune, bool) {
	if len(input) < 4 {
		return 0, false
	}
	value, err := strconv.ParseUint(string(input[:4]), 16, 16)
	return rune(value), err == nil
}
