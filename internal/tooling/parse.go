package tooling

import (
	"encoding/json"
	"fmt"
)

// ParseArgs unmarshals JSON tool arguments into a typed struct T.
func ParseArgs[T any](argsJSON string) (T, error) {
	var v T
	if err := json.Unmarshal([]byte(argsJSON), &v); err != nil {
		return v, fmt.Errorf("parse args: %w", err)
	}
	return v, nil
}
