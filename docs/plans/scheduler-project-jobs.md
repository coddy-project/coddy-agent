# Project scheduler jobs and their trust

Design record, 2026-10-06. Scheduler jobs used to live in one folder,
`scheduler.dir` (default `${CODDY_HOME}/scheduler`), and every job in it ran.
This change adds **project jobs**: job files committed to a repository under
`<workspace>/.coddy/scheduler/*.md`, so a team can carry its scheduled work
with the code. A project job is repository content that would run an agent
with the operator's permissions on a timer, so it gets the trust gate project
MCP servers, subagent definitions and hooks have: it does not run until the
operator approves it, and an approval is bound to the file's content.

The plan went through one cross-review round before implementation; the
decisions it changed are folded in below.

## 1. What the operator gets

- Two fixed job roots, nothing to configure:
  - **user** jobs: `${CODDY_HOME}/scheduler/*.md`;
  - **project** jobs: `<workspace>/.coddy/scheduler/*.md`, where the workspace
    is the session's cwd canonicalised by `mcp.CanonicalWorkspace` (absolute,
    links resolved; no search for a repository root, the same rule as project
    subagents and hooks: a session opened in a subfolder sees that
    subfolder's `.coddy/scheduler`).
- `scheduler.dir` is removed (section 7).
- A project job **created through Coddy** (web UI, REST, the
  `coddy_scheduler_job_create` tool) is trusted at once for this operator: the
  create receipts the bytes it wrote. The same file reaching another operator
  through git is not trusted for them: their scheduler drawer shows it with a
  "needs approval" shield, and it neither runs on cron nor by hand until
  approved.
- An approved job whose file changes afterwards (a `git pull`, a hand edit)
  falls back to "needs approval": the receipt is bound to the digest of the
  bytes. Edits made through Coddy to a job that was trusted re-issue the
  receipt for the bytes written.
- Every trusted job - user or project, from any known workspace - runs in the
  one scheduler daemon of the process (`coddy serve`, or `coddy acp
  --scheduler`, which follows the same rules), in the one background task
  pool, under the one `scheduler.max_queue` and `scheduler.timeout`.
- Ids never clash across scopes: a project job may not take the id of a user
  job, and a user job may not take the id of a project job of a known
  workspace (409 on create and rename, both directions). A project file that
  arrives with a clashing id anyway is shown as **conflict**, never runs and
  cannot be approved until renamed.

## 2. Job identity and snapshots

```go
// external/scheduler/storage
type JobRef struct {
    Scope     string // "user" | "project"
    Workspace string // canonical workspace; "" for user scope
    ID        string // basename without .md
    Path      string // *.md path (never a link, see section 4)
    StatePath string // the .state sidecar (section 2.2)
}

// One read of a job file: what trust is decided on is what runs.
type JobSnapshot struct {
    Ref    JobRef
    Raw    []byte
    Digest string // "sha256:" + hex(sha256(Raw))
    FM     *JobFrontmatter
    Body   string
}
```

### 2.1 Addressing

- `job_id` stays the path segment of every route. Without `scope` a route
  addresses the user job, so every existing client keeps working.
- `scope=project` addresses a project job. Its workspace is:
  - the `workspace=<path>` query when given, accepted only when it names a
    workspace already in the daemon's scan set (section 3.4) - the drawer
    links to project jobs of other known workspaces this way; anything else is
    403;
  - otherwise the session in `X-Coddy-Session-ID` through `resolveSessionCWD`
    (the server's default cwd without the header), the rule every route that
    changes a workspace follows (`.cursor/rules/api-layer.mdc`). A raw `cwd`
    query is never accepted on a mutating route.
- The running map of the daemon is keyed by the canonical `*.md` path; with
  links refused under the project root (section 4) two workspaces never share
  one, and a project job cannot alias a user job.

### 2.2 State sidecar

A project job's `.state` lives in the operator's home, never in the checkout:
`${CODDY_HOME}/scheduler/.projects/<key>/<job_id>.state`, `<key>` = first 16
hex chars of sha256(canonical workspace). User jobs keep `<id>.state` next to
`<id>.md`. `StatePath` becomes a field of `JobRef`; `RunRequest` and the
`Runtime` methods (`StartRun`, `CancelRun`, `RunningRun`, `ClearRuns`,
`DeleteJobHistory`) take a `JobRef` instead of a bare path, so no call site
can derive the state path next to the `.md` again. A test asserts that no
`*.state` ever appears under a workspace.

### 2.3 Job sessions

The job session records the job's scope and workspace in `session.json` next
to `schedulerJobId`. The bundle-walk fallback of `JobSessionIDFor` (find the
job session when the sidecar is missing) matches scope and workspace too, so
a user job never adopts a project job's session or its retention, and vice
versa.

## 3. Trust

### 3.1 Store

`${CODDY_HOME}/scheduler-trust.json`, a sibling of `mcp-trust.json`,
`subagents-trust.json` and `hooks-trust.json` (one kind of approval never
reads as another), written like the hooks store (in-process mutex per path,
`platform.LockFile` on `<file>.lock`, unique temp file and rename; an
unreadable file reads as empty, as there):

```json
{"version": 1, "workspaces": {"/home/me/src/app": [
  {"job_id": "nightly-tests", "digest": "sha256:...", "approved_at": "..."}]}}
```

### 3.2 Policy

New key `scheduler.project_trust`: `ask` (default) | `allow` | `deny`, the
vocabulary of `hooks.project_trust` and `mcp.project_trust`. It is read live on
every tick and request, so it is not part of the daemon's rebuild fingerprint.

| State            | When                                                           |
|------------------|----------------------------------------------------------------|
| `conflict`       | project job whose id is also a user job (checked first)        |
| `denied`         | project job under `deny`                                       |
| `trusted`        | user job; project job under `allow`; project job under `ask` whose receipt matches the snapshot's digest |
| `needs_approval` | project job under `ask` without a matching receipt             |

Only `trusted` jobs run. Trust is decided on a `JobSnapshot` and the same
snapshot is what runs: the tick reads each file once, decides conflict and
trust on that read **before** the eligibility check and the checkpoint write,
and hands the snapshot to `StartRun`, which re-checks conflict and trust on it
before reserving a slot (the order `resolveDefinition` already follows). A
manual run of a job that is not trusted is `ErrJobUntrusted` (409, naming the
approval route). A file swapped between the decision and the launch is never
launched with the old decision: the snapshot is the content.

### 3.3 Who writes receipts

- `CreateJob` with `scope: project` receipts the digest of the buffer it wrote
  (with `O_EXCL`), never a re-read: "I created it, so it is mine".
- `ReplaceJob` / `PatchJob` / `PauseJob` / `ResumeJob` / rename: one snapshot
  is read, its trust decided, the patch applied in memory, the buffer written
  (temp file and rename), and the receipt re-issued for the written buffer
  **only if the snapshot was trusted**. An edit through Coddy never launders a
  job that was not approved; pausing a foreign job keeps it
  `needs_approval`. Rename moves the receipt with the file.
- `DeleteJob` removes the receipt.
- `POST /coddy/scheduler/jobs/{job_id}/trust?scope=project` with
  `{"digest": "sha256:..."}`: the digest the client showed must equal the
  current snapshot's (409 otherwise), so the operator approves exactly the
  bytes they read. Refused (409) for a `conflict` job, 400 under `deny` and
  for user scope. `POST .../untrust` revokes.
- No tool approves repository content: there is no trust tool. A tool create
  is receipted like any create, which grants nothing new - the same tool
  already creates user jobs, trusted and `bypass` by default.

### 3.4 Which workspaces the daemon scans

1. the user root;
2. the process cwd of the daemon's process;
3. every workspace with at least one receipt;
4. every workspace in the registry `${CODDY_HOME}/scheduler-workspaces.json`,
   to which a workspace is added when a session-scoped request touches its
   project jobs (a list with `X-Coddy-Session-ID`, a create, a trust, a tool
   call from a session there). This is what makes a job run under `allow`,
   where no receipt is ever written. A raw `cwd` listing never registers a
   workspace.

A workspace has **no** project jobs when its project folder canonicalises to
the user root or anywhere inside `${CODDY_HOME}`: `coddy serve` started in
`$HOME` (the packaged unit falls back to `%h` when `~/Coddy` is missing) would
otherwise list every user job twice.

## 4. Path containment

For a project job:

- `.coddy`, `.coddy/scheduler` and each `*.md` must be what `os.Lstat` calls a
  directory and a regular file: a link at any of them is refused, on read
  (the job is skipped and listed as invalid with the reason) and on write;
- the canonical project folder must stay under the canonical workspace and
  must not be, or be inside, the user root or `${CODDY_HOME}`;
- create opens with `O_CREATE|O_EXCL`; replace and patch write a temp file in
  the same folder and rename it over the job, so nothing is written through a
  link that appears after the check;
- `cwd:` in the frontmatter must be empty (the workspace) or relative, and
  both its lexical and its canonical form must stay inside the workspace;
  absolute paths and `..` escapes are refused on write and make a file found
  on disk invalid.

User jobs keep today's behaviour.

## 5. Surfaces

### REST (`external/httpserver/scheduler_http.go`, `openapi.go`)

- `GET /coddy/scheduler/jobs`: without a session header or `cwd`, user jobs
  only (the old contract). With either - `cwd` through `resolveListingCWD`,
  read-only - user jobs, then the project jobs of the scan set and of that
  workspace. `scope=user` narrows to user jobs. New row fields: `scope`,
  `workspace`, `trust`, `trust_reason`, `digest`, `scheduled` (false when the
  job is listed but its workspace is outside the scan set, so it will not
  fire). Envelope: `dir` (user root, read-only), `project_dir`
  (`.coddy/scheduler`), `project_trust`, `workspace` (the one the request
  named).
- `GET /coddy/scheduler/jobs/{job_id}` additionally returns `raw` (the file
  text) for a project job: what the approval view shows.
- `POST /coddy/scheduler/jobs`: body gains `scope` (`user` default |
  `project`); the workspace comes from the session (section 2.1).
- Every `/coddy/scheduler/jobs/{job_id}...` route accepts `scope=project` and
  `workspace=` as in 2.1.
- New: `POST .../{job_id}/trust`, `POST .../{job_id}/untrust`.

### Tools (`external/scheduler/tools`)

`scope` (`user` | `project`) on create and on every per-job tool; `project`
means the project of the tool's session cwd. List returns user jobs plus the
session workspace's project jobs with `trust`. No trust tool. Descriptions stop
naming `scheduler.dir`.

### Web UI (`external/ui/src/ui/scheduler`)

- The drawer and `api.ts` get the workspace plumbing subagents have
  (`chatWorkspace` and the session id from `App.tsx`; the list passes the
  session header or `cwd`, per-job calls pass `scope=project` and, for
  another workspace, `workspace=`).
- The drawer groups jobs: "Global", then one heading per workspace ("This
  project" for the chat's). A row that is not trusted shows its state badge
  (needs approval / denied / conflict) and Run is disabled.
- The approval view shows the raw file read-only plus the workspace, and the
  shield approves the digest it showed.
- The editor gets a scope switch (Global / This project) on create, available
  when a session exists; scope is fixed afterwards.
- Hash routes carry the scope: `#/scheduler/jobs/<id>?scope=project&workspace=<path>`
  (same suffix on `/runs`) in `scheduler/hashRoute.ts`.
- Settings → Scheduler loses "Jobs directory" and gains "Project jobs trust".
- EN and RU dictionaries in the same change; `DESIGN.md` and
  `docs/surfaces/web-ui.md` updated; screenshots on the docs page and in the PR.

### Config and docs

`scheduler.project_trust` added and `scheduler.dir` removed in the struct,
`config.schema.json`, `ui_schema.go`, `jsondto.go`, `schema_ui_defaults.go`,
`config.example.yaml`, the `configure-coddy` skill, `docs/operate/scheduler.md`
(a "Where jobs live" section, which `coddy -t` links to),
`docs/getting-started/configuration.md`, `docs/reference/http-api.md`,
`docs/reference/tools.md`, `make docs`. Every other `scheduler.dir` mention
moves too (`internal/dryrun/paths.go`, the `coddy serve` banner,
`schedulerFingerprint`, `daemon/start.go`, `service.go`, comments in
`internal/agent` and `internal/session`, `examples/`). The site copy of the
schema is held until the release, since `additionalProperties: false` marks
configs on disk that still carry the removed key.

## 6. Tests

Happy path, `features/scheduler_project_jobs.feature` (godog over a real
httptest server and the daemon runtime, scripted model):

- a project job created through the API from a session is trusted and runs on
  its cron minute in the shared pool next to a user job, and no `.state`
  appears in the workspace;
- a project job written to disk (as git would) is listed `needs_approval`,
  does not run on its minute, is refused on a manual run; after `trust` with
  the shown digest it runs;
- editing the approved file on disk returns it to `needs_approval`;
- creating a project job with a user job's id is refused with 409.

Unit tests: trust store (approve, revoke, rename, digest mismatch,
concurrent writers); `allow` / `deny` matrix and the registry under `allow`;
trust refused on a conflict and the reverse clash; links at `.coddy`, at
`.coddy/scheduler`, at a job file, dangling; a `$HOME` workspace and one
inside `${CODDY_HOME}`; `cwd:` absolute, `..`, linked; a file swapped between
the decision and the launch; a patch of an untrusted job issues no receipt
while a tool create in bypass is receipted; the job-session fallback walk by
scope; the legacy `scheduler.dir` migration (collision, `.state` following its
`.md`, a workspace `config.yaml` left alone); UI vitest for the grouping, the
badges, the approval view and the scope switch.

## 7. Removing `scheduler.dir`

`scheduler.dir` joins `movedKeys` (a warning in `coddy -t`) and
`migrateLegacyKeys`: on the next start the key is cut from `config.yaml`
(backup `config.yaml.bak-<time>`). When it pointed anywhere other than
`${CODDY_HOME}/scheduler`, each `*.md` job is **copied** into
`${CODDY_HOME}/scheduler` with its `.state`:

- a name the target does not have is copied as it is;
- a name the target has with the same bytes is skipped;
- a name the target has with other bytes is copied as `<id>-migrated.md`
  (and `<id>-migrated.state`), so no job silently stops.

The source folder is left as it was. A copy that fails leaves the key in place
(logged, still not read). The existing `ConfigFromWorkspace` guard applies: a
`config.yaml` read from the workspace never has its jobs copied.

## 8. Out of scope

- A console (TUI) surface and a `coddy scheduler trust` CLI; the web UI and
  REST carry approvals, and the daemon logs once per digest it skips.
- Garbage collection of `.projects/<key>` state of workspaces that are gone.
- Discovering project jobs in workspaces Coddy has never had a session in.
