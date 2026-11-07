package llm

import (
	"errors"
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

func setKimiIdentityHeaders(req *http.Request, deviceID string) error {
	if deviceID == "" {
		var err error
		deviceID, err = kimiDeviceID()
		if err != nil {
			return errors.New("kimi: cannot generate device identifier")
		}
	}
	if !kimiDeviceIDPattern.MatchString(deviceID) {
		return errors.New("kimi: invalid device identifier")
	}
	v := version.Get()
	req.Header.Set("User-Agent", "coddy/"+v)
	req.Header.Set("X-Msh-Platform", "kimi_code_cli")
	req.Header.Set("X-Msh-Version", v)
	req.Header.Set("X-Msh-Device-Id", deviceID)
	return nil
}
