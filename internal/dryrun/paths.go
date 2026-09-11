package dryrun

import (
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
)

// paths checks every filesystem location the configuration names. A
// directory the process creates itself at start (sessions, scheduler,
// memory, the log file's folder) is fine when missing as long as the path
// runs through directories; a directory the process only reads (prompts,
// skills, subagents, hooks) has to be there, and is a warning when it is an
// entry the operator wrote and a silent skip when it is a default.
func (r *runner) paths() {
	cfg := r.req.Cfg
	cwd := r.req.Paths.CWD

	r.rep.add(r.creatableDir("sessions.dir", cfg.ResolvedSessionsRoot()))

	if hasOutput(cfg.Logger.Outputs, config.LogOutputFile) && strings.TrimSpace(cfg.Logger.File) != "" {
		r.rep.add(r.creatableFile("logger.file", cfg.Logger.File))
	}

	if dir := cfg.Prompts.ResolvedDir(cwd); dir != "" {
		st, err := os.Stat(dir)
		switch {
		case err != nil:
			r.rep.add(r.check(StatusError, "prompts.dir", "prompts.dir", dir+" does not exist", "create it, or remove prompts.dir to use the built-in templates"))
		case !st.IsDir():
			r.rep.add(r.check(StatusError, "prompts.dir", "prompts.dir", dir+" is not a directory", "point prompts.dir at the folder holding the templates"))
		default:
			r.rep.add(r.check(StatusOK, "prompts.dir", "prompts.dir", dir+" exists", ""))
			for key, file := range map[string]string{
				"prompts.agent_prompt": cfg.Prompts.AgentFile(),
				"prompts.plan_prompt":  cfg.Prompts.PlanFile(),
				"prompts.ask_prompt":   cfg.Prompts.AskFile(),
			} {
				if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
					r.rep.add(r.check(StatusWarning, key, key, file+" not found in "+dir+"; the built-in template is used instead", "add the file or drop the key"))
				}
			}
		}
	}

	for i, d := range cfg.Skills.Dirs {
		r.readableDir(fmt.Sprintf("skills.dirs[%d]", i), config.ExpandCWD(d, cwd))
	}
	for i, d := range cfg.Subagents.Dirs {
		r.readableDir(fmt.Sprintf("subagents.dirs[%d]", i), config.ExpandCWD(d, cwd))
	}
	for i, f := range cfg.Hooks.Files {
		r.hookFile(fmt.Sprintf("hooks.files[%d]", i), config.ExpandCWD(f, cwd))
	}

	if cfg.SchedulerEffectiveEnabled() && strings.TrimSpace(cfg.Scheduler.Dir) != "" {
		r.rep.add(r.creatableDir("scheduler.dir", cfg.Scheduler.Dir))
	}
	if cfg.Memory.Enabled && strings.TrimSpace(cfg.Memory.Dir) != "" {
		r.rep.add(r.creatableDir("memory.dir", cfg.Memory.Dir))
	}

	if cfg.Swarm.Enabled && cfg.Swarm.TLS.Enabled() {
		if _, err := tls.LoadX509KeyPair(cfg.Swarm.TLS.CertFile, cfg.Swarm.TLS.KeyFile); err != nil {
			r.rep.add(r.check(StatusError, "swarm.tls", "swarm.tls", "certificate and key do not load: "+err.Error(), "check swarm.tls.cert_file and swarm.tls.key_file"))
		} else {
			r.rep.add(r.check(StatusOK, "swarm.tls", "swarm.tls", "certificate and key load", ""))
		}
	}
}

func hasOutput(outputs []string, want string) bool {
	for _, o := range outputs {
		if strings.EqualFold(strings.TrimSpace(o), want) {
			return true
		}
	}
	return false
}

// creatableDir accepts a directory that exists or that can come into being:
// the process creates it at start, so only a path blocked by a regular file
// is a problem.
func (r *runner) creatableDir(path, dir string) Check {
	st, err := os.Stat(dir)
	switch {
	case err == nil && st.IsDir():
		return r.check(StatusOK, path, path, dir+" exists", "")
	case err == nil:
		return r.check(StatusError, path, path, dir+" is not a directory", "point "+path+" at a folder")
	}
	if blocker := blockingFile(dir); blocker != "" {
		return r.check(StatusError, path, path, fmt.Sprintf("%s cannot be created: %s is a file", dir, blocker), "point "+path+" elsewhere or remove the file")
	}
	return r.check(StatusOK, path, path, dir+" will be created at first start", "")
}

// creatableFile accepts a file that exists and opens for appending, or one
// whose folder can come into being.
func (r *runner) creatableFile(path, file string) Check {
	st, err := os.Stat(file)
	switch {
	case err == nil && st.IsDir():
		return r.check(StatusError, path, path, file+" is a directory", "name a file, not a folder")
	case err == nil:
		f, oerr := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
		if oerr != nil {
			return r.check(StatusError, path, path, file+" cannot be opened for writing: "+shortErr(oerr), "fix the permissions or point "+path+" elsewhere")
		}
		_ = f.Close()
		return r.check(StatusOK, path, path, file+" is writable", "")
	}
	if blocker := blockingFile(filepath.Dir(file)); blocker != "" {
		return r.check(StatusError, path, path, fmt.Sprintf("%s cannot be created: %s is a file", file, blocker), "point "+path+" elsewhere or remove the file")
	}
	return r.check(StatusOK, path, path, file+" will be created at first start", "")
}

// blockingFile walks up from dir and returns the first existing ancestor that
// is a regular file, which no MkdirAll can get past; "" when the path can be
// created.
func blockingFile(dir string) string {
	for cur := filepath.Clean(dir); ; {
		st, err := os.Stat(cur)
		if err == nil {
			if st.IsDir() {
				return ""
			}
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// readableDir reports a directory the process only reads: an entry the
// operator wrote has to exist; a default that is absent is nothing.
func (r *runner) readableDir(path, dir string) {
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		r.rep.add(r.check(StatusOK, path, path, dir+" exists", ""))
		return
	}
	if !r.explicit(path) {
		return
	}
	r.rep.add(r.check(StatusWarning, path, path, dir+" does not exist",
		"create it or remove the entry; a ${CWD} entry is resolved per session, so a folder missing here may exist in another workspace"))
}

// hookFile parses a hook definition file the way the loader does.
func (r *runner) hookFile(path, file string) {
	data, err := os.ReadFile(file)
	if err != nil {
		if r.explicit(path) {
			r.rep.add(r.check(StatusWarning, path, path, file+" does not exist", "create it or remove the entry"))
		}
		return
	}
	if _, perr := hooks.Parse(data); perr != nil {
		r.rep.add(r.check(StatusError, path, path, file+" is invalid: "+perr.Error(), "fix the JSON (Claude Code's hooks shape, see docs/hooks.md)"))
		return
	}
	r.rep.add(r.check(StatusOK, path, path, file+" parses", ""))
}

// caFileCheck reads a PEM bundle and counts its certificates.
func (r *runner) caFileCheck(path, file string) Check {
	data, err := os.ReadFile(file)
	if err != nil {
		return r.check(StatusError, path, path, "cannot read "+file+": "+shortErr(err), "point "+path+" at a PEM bundle of CA certificates")
	}
	n := 0
	for rest := data; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			n++
		}
	}
	if n == 0 {
		return r.check(StatusError, path, path, "no certificate found in "+file, path+" must be a PEM bundle of CA certificates")
	}
	return r.check(StatusOK, path, path, fmt.Sprintf("%s holds %s", file, plural(n, "certificate")), "")
}

// mcpCommands resolves the executable of every stdio MCP server of
// config.yaml in PATH, the way the spawn would, without spawning it.
func (r *runner) mcpCommands() {
	for i := range r.req.Cfg.MCPServers {
		srv := &r.req.Cfg.MCPServers[i]
		path := "mcp_servers[" + srv.Name + "]"
		cmd := strings.TrimSpace(srv.Command)
		switch {
		case srv.Disabled:
			r.rep.add(r.check(StatusSkipped, path, path, "disabled in config", ""))
		case cmd == "" && strings.TrimSpace(srv.URL) == "":
			r.rep.add(r.check(StatusError, path, path, "neither command nor url is set", "give the server a command (stdio) or a url (http)"))
		case cmd != "":
			resolved, err := exec.LookPath(cmd)
			if err != nil {
				fix := "install it or write an absolute path in " + path + ".command"
				if strings.ContainsAny(cmd, " \t") {
					fix = "command must be the executable alone; put its arguments in " + path + ".args"
				}
				r.rep.add(r.check(StatusError, path, path+".command", fmt.Sprintf("command %q not found in PATH", cmd), fix))
				continue
			}
			r.rep.add(r.check(StatusOK, path, path+".command", fmt.Sprintf("command %q resolves to %s", cmd, resolved), ""))
		}
	}
}
