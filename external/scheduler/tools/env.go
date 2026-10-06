//go:build scheduler

package schedtools

import (
	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"

	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func toolEnvCWD(env *tooling.Env) string {
	if env == nil {
		return ""
	}
	return strings.TrimSpace(env.CWD)
}

// toolService is the scheduler service acting for the tool's session: the
// session cwd is the workspace a project job of the call lives in, while the
// process cwd stays the daemon's (the workspace it scans whatever the session).
func toolService(cfg *config.Config, env *tooling.Env) *schedservice.Service {
	processCWD := ""
	if cfg != nil {
		processCWD = cfg.Paths.CWD
	}
	return schedservice.NewService(cfg, nil, processCWD).WithSession(toolEnvCWD(env))
}

// toolAddr addresses the job a call names: a user job, or with scope project
// the project job of the session's workspace.
func toolAddr(op *schedservice.Service, scope, jobID string) (schedservice.JobAddr, error) {
	s, err := schedservice.NormalizeScope(scope)
	if err != nil {
		return schedservice.JobAddr{}, err
	}
	id := strings.TrimSpace(jobID)
	if s == "project" {
		return op.ProjectJob(id, "")
	}
	return schedservice.UserJob(id), nil
}

// scopeProperty is the scope argument every per-job tool takes.
func scopeProperty() map[string]interface{} {
	return map[string]interface{}{
		"type": "string",
		"enum": []interface{}{"user", "project"},
		"description": "user (default): a job in ${CODDY_HOME}/scheduler. project: a job in <session cwd>/.coddy/scheduler, " +
			"which travels with the repository and runs only once the operator approved it (a project job you create is approved at once).",
	}
}
