//go:build scheduler

package schedservice

import (
	"encoding/json"
	"errors"
	"io"
)

// DecodeSchedulerJobPatch reads PATCH JSON (empty body yields zero patch).
func DecodeSchedulerJobPatch(r io.Reader) (SchedulerJobPatch, error) {
	var p SchedulerJobPatch
	dec := json.NewDecoder(r)
	if err := dec.Decode(&p); err != nil {
		if errors.Is(err, io.EOF) {
			return SchedulerJobPatch{}, nil
		}
		return SchedulerJobPatch{}, err
	}
	return p, nil
}
