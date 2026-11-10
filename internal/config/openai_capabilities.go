package config

import "strings"

// OpenAIResponsesModel identifies models whose tool calling needs Responses.
// A deployment's explicit protocol setting takes precedence over this fallback.
func OpenAIResponsesModel(model string) bool {
	return openAIFamily(model, "gpt-6-astra", "gpt-6.1-sol", "gpt-6-luna")
}

func openAIFamily(model string, names ...string) bool {
	id := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	for _, name := range names {
		if id == name || strings.HasPrefix(id, name+"-") {
			return true
		}
	}
	return false
}
