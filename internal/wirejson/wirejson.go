// Package wirejson turns Go values into the JSON the clients see: camelCase
// field names, snake_case type names, and map keys (such as player ids) left
// exactly as they are.
package wirejson

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// Convert converts a message into plain JSON-ready data with camelCase field
// names ("PointsLeft" becomes "pointsLeft", "ID" becomes "id"). Map keys, such
// as player ids, are left exactly as they are. Types that know how to marshal
// themselves (times, phases, raw JSON) are passed through.
func Convert(v any) any { return convertValue(reflect.ValueOf(v)) }

func convertValue(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if t := v.Type(); t.Implements(jsonMarshalerType) || t.Implements(textMarshalerType) {
		if v.Kind() == reflect.Pointer && v.IsNil() {
			return nil
		}
		return v.Interface()
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return convertValue(v.Elem())
	case reflect.Struct:
		out := map[string]any{}
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			if f := t.Field(i); f.IsExported() {
				out[LowerFirst(f.Name)] = convertValue(v.Field(i))
			}
		}
		return out
	case reflect.Slice, reflect.Array:
		// A nil slice is still a list: clients get [] and never have to
		// check for null.
		out := make([]any, v.Len())
		for i := range out {
			out[i] = convertValue(v.Index(i))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		out := make(map[string]any, v.Len())
		for it := v.MapRange(); it.Next(); {
			out[fmt.Sprint(it.Key().Interface())] = convertValue(it.Value())
		}
		return out
	}
	return v.Interface()
}

// lowerFirst lowercases the leading capitals of a Go name: "PointsLeft" to
// "pointsLeft", "ID" to "id", "HTTPServer" to "httpServer".
func LowerFirst(s string) string {
	r := []rune(s)
	n := 0
	for n < len(r) && unicode.IsUpper(r[n]) {
		n++
	}
	if n > 1 && n < len(r) {
		n-- // keep the capital that starts the next word
	}
	for i := 0; i < n; i++ {
		r[i] = unicode.ToLower(r[i])
	}
	return string(r)
}

func TypeName(v any) string {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}

// snake turns "MemberJoined" into "member_joined".
func Snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
