package acp

import "unicode/utf8"

// MaxQuestionErrorBytes bounds client diagnostics carried into a question tool
// result. It does not limit successful answers or the surrounding error context.
const MaxQuestionErrorBytes = 4096

// BoundQuestionError preserves short diagnostics and marks longer ones as
// truncated, keeping the marker within the byte budget and UTF-8 runes intact.
func BoundQuestionError(message string) string {
	if len(message) <= MaxQuestionErrorBytes {
		return message
	}
	const marker = " [truncated]"
	end := MaxQuestionErrorBytes - len(marker)
	for end > 0 && !utf8.RuneStart(message[end]) {
		end--
	}
	return message[:end] + marker
}
