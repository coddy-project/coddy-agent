//go:build browser

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Transfer runs upload_file or download_file. The bytes never ride the
// socket: they wait under a one-time ticket the extension fetches (an
// upload) or posts to (a download) on the endpoint's files route. upload is
// the file for upload_file and nil for download_file, whose bytes come back.
func (h *Hub) Transfer(ctx context.Context, req Request, filename string, upload []byte) (json.RawMessage, []byte, error) {
	toPage := req.Action == "upload_file"
	if !toPage && req.Action != "download_file" {
		return nil, nil, errors.New("only upload_file and download_file move files")
	}
	ticket, cleanup, err := StageFile(upload, toPage, actionTimeout(req.Action)+30*time.Second)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()
	params := map[string]any{}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil || params == nil {
			return nil, nil, errors.New("params must be an object")
		}
	}
	params["transfer_id"], params["filename"] = ticket, filename
	if req.Params, err = json.Marshal(params); err != nil {
		return nil, nil, err
	}
	out, err := h.Call(ctx, req)
	if err != nil || toPage {
		return out, nil, err
	}
	got, err := FileResult(ticket)
	if err != nil {
		return nil, nil, err
	}
	return out, got, nil
}
