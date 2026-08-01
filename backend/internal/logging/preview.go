package logging

import "unicode/utf8"

const defaultPreviewRunes = 512

// Preview returns a truncated UTF-8 preview of value for Debug logs.
// When value exceeds max runes it appends an ellipsis. A non-positive max
// uses the default preview length.
func Preview(value string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = defaultPreviewRunes
	}
	if value == "" {
		return ""
	}
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	return string([]rune(value)[:maxRunes]) + "…"
}
