// Package jsonutil provides helpers for encoding values as JSON strings.
package jsonutil

import (
	"encoding/json"

	"github.com/samber/mo"
)

// MustJSON returns the JSON encoding of value as a string.
// It panics if encoding fails; callers should pass values expected to marshal cleanly,
// such as plain structs, maps, and slices used in structured logs.
func MustJSON(value any) string {
	return string(mo.TupleToResult(json.Marshal(value)).MustGet())
}
