package config

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
)

var extensionValueOwner = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
var extensionValueKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func cloneExtensionValues(source map[string]map[string]any) map[string]map[string]any {
	if source == nil {
		return nil
	}
	out := make(map[string]map[string]any, len(source))
	for owner, values := range source {
		out[owner] = make(map[string]any, len(values))
		for key, value := range values {
			out[owner][key] = value
		}
	}
	return out
}

func validateExtensionValues(values map[string]map[string]any) error {
	for owner, fields := range values {
		if len(owner) > 32 || !extensionValueOwner.MatchString(owner) {
			return fmt.Errorf("extension_values: invalid extension id %q", owner)
		}
		if len(fields) > 128 {
			return fmt.Errorf("extension_values.%s: at most 128 fields", owner)
		}
		for key, value := range fields {
			if !extensionValueKey.MatchString(key) {
				return fmt.Errorf("extension_values.%s: invalid field id %q", owner, key)
			}
			switch v := value.(type) {
			case nil, string, bool, int, int64, uint64:
			case float64:
				if math.IsNaN(v) || math.IsInf(v, 0) {
					return fmt.Errorf("extension_values.%s.%s: expected a finite number", owner, key)
				}
			default:
				return fmt.Errorf("extension_values.%s.%s: expected a string, number, boolean or null", owner, key)
			}
		}
	}
	data, err := json.Marshal(values)
	if err != nil || len(data) > 256<<10 {
		return fmt.Errorf("extension_values: values exceed 256 KiB or cannot be encoded")
	}
	return nil
}
