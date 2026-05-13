# UI Model & Provider Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow users to add, edit, and delete LLM providers and models directly from the Web UI without manually editing `config.yaml`.

**Architecture:** Introduce a runtime overlay config file (`$CODDY_HOME/ui-config.yaml`) that stores UI-managed providers and models. The existing static `config.yaml` remains untouched; at load time the server merges static + runtime configs. New REST endpoints under `/coddy/admin/providers` and `/coddy/admin/models` handle CRUD. A new settings modal in the React UI provides forms for provider/model management. API keys are masked on read and only overwritten when explicitly supplied.

**Tech Stack:** Go 1.22+, gopkg.in/yaml.v3, React 19 + TypeScript, native `fetch()`.

---

## File Map

| File | Responsibility |
|------|----------------|
| `internal/config/runtime.go` | Runtime overlay config types, load/save helpers, merge logic. |
| `internal/config/config.go` | Hook runtime overlay into `readConfigFile` / `LoadFromCLI`. |
| `internal/config/resolve.go` | Update `FindModelEntry` / `FindProvider` to search merged lists. |
| `external/httpserver/admin_config.go` | New HTTP handlers for provider & model CRUD. |
| `external/httpserver/coddy_coddy.go` | Register new admin routes in `registerCoddyRoutes()`. |
| `external/httpserver/server.go` | Inject runtime overlay reference into `Server` struct. |
| `external/ui/src/ui/settings/SettingsModal.tsx` | Settings modal shell with tab navigation. |
| `external/ui/src/ui/settings/ProviderForm.tsx` | Form to add/edit a provider. |
| `external/ui/src/ui/settings/ModelForm.tsx` | Form to add/edit a model. |
| `external/ui/src/ui/settings/api.ts` | Typed `fetch` wrappers for admin endpoints. |
| `external/ui/src/ui/App.tsx` | Mount modal, hold admin state, refresh `/v1/models` after mutation. |
| `external/ui/src/ui/nav/NavRail.tsx` | Add settings button that opens modal. |
| `external/ui/src/ui/chat/Composer.tsx` | Add small "Manage models…" link in model dropdown. |

---

### Task 1: Runtime Overlay Schema & I/O

**Files:**
- Create: `internal/config/runtime.go`

- [ ] **Step 1: Define runtime types**

Create `RuntimeOverlay` struct that mirrors the shape we need:
```go
type RuntimeOverlay struct {
    Providers []ProviderConfig `yaml:"providers,omitempty"`
    Models    []ModelEntry     `yaml:"models,omitempty"`
}
```

Add helpers:
- `RuntimeOverlayPath(home string) string` → `filepath.Join(home, "ui-config.yaml")`
- `LoadRuntimeOverlay(path string) (*RuntimeOverlay, error)` — return empty overlay if file missing.
- `SaveRuntimeOverlay(path string, o *RuntimeOverlay) error` — write YAML with `0600` permissions.

- [ ] **Step 2: Write unit test for load/save round-trip**

Create `internal/config/runtime_test.go`.
Test saves an overlay with one provider and one model, reloads it, and asserts equality.

Run: `go test ./internal/config -run TestRuntimeOverlayRoundTrip -v`
Expected: PASS after implementation.

- [ ] **Step 3: Implement runtime.go**

Use `yaml.v3` Marshal/Unmarshal. Ensure empty slices are omitted on save.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config -run TestRuntimeOverlay -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/runtime.go internal/config/runtime_test.go
git commit -m "feat(config): runtime overlay types and I/O"
```

---

### Task 2: Merge Runtime Overlay into Config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/types.go`
- Modify: `internal/config/resolve.go`

- [ ] **Step 1: Add overlay fields to Config**

In `types.go`, add to `Config`:
```go
RuntimeOverlay      *RuntimeOverlay `yaml:"-"`
RuntimeOverlayPath  string          `yaml:"-"`
```

- [ ] **Step 2: Merge after static load**

In `config.go` `readConfigFile`, after `applyDefaults(cfg)`:
1. Derive runtime path: `rp := RuntimeOverlayPath(paths.Home)` (or from a new `Paths.RuntimeOverlay` field if you prefer).
2. `overlay, _ := LoadRuntimeOverlay(rp)` — log error but do not fail startup.
3. Assign `cfg.RuntimeOverlay = overlay` and `cfg.RuntimeOverlayPath = rp`.

- [ ] **Step 3: Update resolution helpers**

In `resolve.go`:
- `FindProvider(name string)`: search `cfg.Providers` first, then `cfg.RuntimeOverlay.Providers`.
- `FindModelEntry(model string)`: search `cfg.Models` first, then `cfg.RuntimeOverlay.Models`.
- `AllProviders()`: concat static + runtime providers.
- `AllModels()`: concat static + runtime models.

- [ ] **Step 4: Write tests for merged lookup**

In `internal/config/resolve_test.go` (or `config_test.go`):
```go
func TestFindModelEntryRuntimeOverlay(t *testing.T) {
    cfg := &Config{
        Models: []ModelEntry{{Model: "static/m1"}},
        RuntimeOverlay: &RuntimeOverlay{
            Models: []ModelEntry{{Model: "runtime/m2"}},
        },
    }
    // assert static found
    // assert runtime found
    // assert missing returns error
}
```

Run: `go test ./internal/config -run TestFindModelEntryRuntimeOverlay -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/types.go internal/config/resolve.go internal/config/*_test.go
git commit -m "feat(config): merge runtime overlay into static config"
```

---

### Task 3: Admin HTTP Handlers (Providers)

**Files:**
- Create: `external/httpserver/admin_config.go`

- [ ] **Step 1: Define request/response DTOs**

In `admin_config.go`:
```go
type adminProvider struct {
    Name    string `json:"name"`
    Type    string `json:"type"`
    APIBase string `json:"api_base"`
    APIKey  string `json:"api_key,omitempty"`
}

type adminModel struct {
    Model            string  `json:"model"`
    MaxTokens        int     `json:"max_tokens"`
    Temperature      float64 `json:"temperature"`
    MaxContextTokens int     `json:"max_context_tokens"`
}
```

Add mask helper: `maskKey(k string) string` — returns `""` if empty, else `"..." + last4`.

- [ ] **Step 2: Implement provider CRUD handlers**

Methods on `*Server`:
- `handleAdminProvidersGet(w,r)` — list all runtime providers with masked keys.
- `handleAdminProviderPost(w,r)` — create; validate no duplicate name; append to overlay; save.
- `handleAdminProviderPut(w,r)` — update by `{name}`; if `api_key` omitted/empty, preserve existing key.
- `handleAdminProviderDelete(w,r)` — delete by `{name}`; also delete any runtime models that reference this provider (to avoid dangling refs).

Validation rules (reuse existing validation where possible):
- `Name` required, unique across static + runtime.
- `Type` must be `"openai"` or `"anthropic"`.

- [ ] **Step 3: Write handler tests**

Create `external/httpserver/admin_config_test.go`.
Spin up a `Server` with a temporary `CODDY_HOME`, exercise POST / GET / PUT / DELETE for providers.

Run: `go test ./external/httpserver -run TestAdminProviderCRUD -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add external/httpserver/admin_config.go external/httpserver/admin_config_test.go
git commit -m "feat(httpserver): admin provider CRUD handlers"
```

---

### Task 4: Admin HTTP Handlers (Models)

**Files:**
- Modify: `external/httpserver/admin_config.go`

- [ ] **Step 1: Implement model CRUD handlers**

Methods on `*Server`:
- `handleAdminModelsGet(w,r)` — list all runtime models.
- `handleAdminModelPost(w,r)` — create; validate `model` is `provider/name` format; provider must exist (static or runtime); no duplicate ID.
- `handleAdminModelPut(w,r)` — update by `{id}` (URL-encoded, e.g. `runtime%2Fgpt-4o`). Re-validate provider.
- `handleAdminModelDelete(w,r)` — delete by `{id}`.

- [ ] **Step 2: Write handler tests**

In `external/httpserver/admin_config_test.go`, add `TestAdminModelCRUD`.

Run: `go test ./external/httpserver -run TestAdminModelCRUD -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add external/httpserver/admin_config.go external/httpserver/admin_config_test.go
git commit -m "feat(httpserver): admin model CRUD handlers"
```

---

### Task 5: Wire Routes & Update `GET /v1/models`

**Files:**
- Modify: `external/httpserver/coddy_coddy.go`
- Modify: `external/httpserver/server.go`
- Modify: `external/httpserver/models.go` (or wherever `handleModels` lives)

- [ ] **Step 1: Register admin routes**

In `registerCoddyRoutes()`, add:
```go
s.mux.HandleFunc("GET /coddy/admin/providers", s.handleAdminProvidersGet)
s.mux.HandleFunc("POST /coddy/admin/providers", s.handleAdminProviderPost)
s.mux.HandleFunc("PUT /coddy/admin/providers/{name}", s.handleAdminProviderPut)
s.mux.HandleFunc("DELETE /coddy/admin/providers/{name}", s.handleAdminProviderDelete)

s.mux.HandleFunc("GET /coddy/admin/models", s.handleAdminModelsGet)
s.mux.HandleFunc("POST /coddy/admin/models", s.handleAdminModelPost)
s.mux.HandleFunc("PUT /coddy/admin/models/{id}", s.handleAdminModelPut)
s.mux.HandleFunc("DELETE /coddy/admin/models/{id}", s.handleAdminModelDelete)
```

- [ ] **Step 2: Ensure `handleModels` includes runtime models**

`handleModels` currently reads from `s.cfg.Models`. Change it to use `s.cfg.AllModels()` (or build the list from static + runtime). Confirm `agent` and `plan` profiles remain first.

- [ ] **Step 3: Add a runtime reload mechanism**

Because the overlay is loaded once at startup, mutations via admin API must update the in-memory `s.cfg.RuntimeOverlay` as well as the file. The handlers in Task 3/4 already do this (mutate overlay then save). No extra work needed if you mutated the same pointer.

Double-check `Server.cfg` is a pointer and the overlay pointer is shared.

- [ ] **Step 4: Run existing HTTP tests**

Run: `go test ./external/httpserver -v`
Expected: All existing tests still PASS.

- [ ] **Step 5: Commit**

```bash
git add external/httpserver/coddy_coddy.go external/httpserver/server.go external/httpserver/models.go
git commit -m "feat(httpserver): wire admin routes and include runtime models in /v1/models"
```

---

### Task 6: UI — Admin API Client

**Files:**
- Create: `external/ui/src/ui/settings/api.ts`

- [ ] **Step 1: Write typed wrappers**

```ts
export interface AdminProvider {
  name: string;
  type: "openai" | "anthropic";
  api_base: string;
  api_key?: string;
}

export interface AdminModel {
  model: string;
  max_tokens: number;
  temperature: number;
  max_context_tokens: number;
}

export async function listProviders(): Promise<{ ok: true; data: AdminProvider[] } | { ok: false; status: number; message: string }>;
export async function createProvider(p: AdminProvider): Promise<...>;
export async function updateProvider(name: string, p: AdminProvider): Promise<...>;
export async function deleteProvider(name: string): Promise<...>;

export async function listModels(): Promise<...>;
export async function createModel(m: AdminModel): Promise<...>;
export async function updateModel(id: string, m: AdminModel): Promise<...>;
export async function deleteModel(id: string): Promise<...>;
```

Reuse the same `ApiResult<T>` pattern from `external/ui/src/ui/scheduler/api.ts`.

- [ ] **Step 2: Commit**

```bash
git add external/ui/src/ui/settings/api.ts
git commit -m "feat(ui): admin config API client"
```

---

### Task 7: UI — Settings Modal Shell

**Files:**
- Create: `external/ui/src/ui/settings/SettingsModal.tsx`

- [ ] **Step 1: Build modal component**

Props:
```ts
interface SettingsModalProps {
  open: boolean;
  onClose: () => void;
}
```

Render a centered dialog overlay (`<dialog>` or `div` with backdrop). Inside:
- Left tab rail: "Providers", "Models".
- Right content pane switches based on active tab.

Use existing CSS variables / tokens from the UI (inspect `external/ui/src/index.css` or existing components for colors/spacing).

- [ ] **Step 2: Commit**

```bash
git add external/ui/src/ui/settings/SettingsModal.tsx
git commit -m "feat(ui): settings modal shell with tabs"
```

---

### Task 8: UI — Provider Management Form

**Files:**
- Create: `external/ui/src/ui/settings/ProviderForm.tsx`

- [ ] **Step 1: Build list + inline form**

Component receives:
```ts
providers: AdminProvider[];
onRefresh: () => void;
```

Render a table/list of providers (name, type, api_base). API key not shown.
Each row has Edit / Delete buttons.

Below the list, an expandable "Add provider" form with fields:
- Name (text)
- Type (select: openai / anthropic)
- API Base (text, placeholder `https://api.openai.com/v1`)
- API Key (password input)

On submit: `createProvider` → `onRefresh()` → clear form.
On edit: populate form, change button to "Save", call `updateProvider`.
On delete: confirm with `window.confirm`, then `deleteProvider` → `onRefresh`.

- [ ] **Step 2: Commit**

```bash
git add external/ui/src/ui/settings/ProviderForm.tsx
git commit -m "feat(ui): provider management form"
```

---

### Task 9: UI — Model Management Form

**Files:**
- Create: `external/ui/src/ui/settings/ModelForm.tsx`

- [ ] **Step 1: Build list + inline form**

Component receives:
```ts
models: AdminModel[];
providers: AdminProvider[];
onRefresh: () => void;
```

List shows `model`, `max_tokens`, `temperature`, `max_context_tokens`.
Add form fields:
- Provider (select from `providers` prop)
- Model ID (text, the part after `/`, e.g. `gpt-4o`)
- Max Tokens (number)
- Temperature (number, step 0.1)
- Max Context Tokens (number)

Concatenate `provider/name` before sending to API.

Edit / Delete logic analogous to Task 8.

- [ ] **Step 2: Commit**

```bash
git add external/ui/src/ui/settings/ModelForm.tsx
git commit -m "feat(ui): model management form"
```

---

### Task 10: UI — Integration into App Shell

**Files:**
- Modify: `external/ui/src/ui/App.tsx`
- Modify: `external/ui/src/ui/nav/NavRail.tsx`
- Modify: `external/ui/src/ui/chat/Composer.tsx`

- [ ] **Step 1: Add modal state to App.tsx**

Add:
```ts
const [settingsOpen, setSettingsOpen] = useState(false);
```

Add a `loadAdminData` callback that fetches both provider and model lists into new state variables (`adminProviders`, `adminModels`). Call it when `settingsOpen` becomes true.

Mount `<SettingsModal open={settingsOpen} onClose={...} />` near the root (next to `ChatScreen` or inside the main layout div).

Pass providers/models into the modal tabs and pass `loadAdminData` as `onRefresh`.

- [ ] **Step 2: Refresh `/v1/models` after mutation**

Inside `loadAdminData`, after fetching admin lists, also re-fetch `/v1/models` and update `llmModelIds` / `modelInfos` state so the Composer dropdown stays in sync.

- [ ] **Step 3: Add settings button to NavRail**

In `NavRail.tsx`, add a bottom icon (gear/cog) that calls `onOpenSettings` prop. Add the prop to `NavRailProps` and wire it in `App.tsx`.

- [ ] **Step 4: Add "Manage models…" entry in Composer dropdown**

In `Composer.tsx`, inside the model dropdown menu (`role="menu"`), add a divider and a final item: "Manage models & providers…". Clicking it calls a new `onOpenSettings` prop (or reuse existing prop if already wired).

- [ ] **Step 5: Build UI**

Run: `cd external/ui && npm run build`
Expected: builds without errors.

- [ ] **Step 6: Commit**

```bash
git add external/ui/src/ui/App.tsx external/ui/src/ui/nav/NavRail.tsx external/ui/src/ui/chat/Composer.tsx
git commit -m "feat(ui): integrate settings modal into app shell"
```

---

### Task 11: Full Build & Regression

**Files:**
- Modify: `external/httpserver/openapi.go` (if you maintain OpenAPI schema manually)
- Modify: `docs/http-api.md`

- [ ] **Step 1: Update OpenAPI spec**

In `external/httpserver/openapi.go`, add paths and schemas for:
- `/coddy/admin/providers`
- `/coddy/admin/models`

- [ ] **Step 2: Update docs**

Add a short section to `docs/http-api.md` describing the admin endpoints.

- [ ] **Step 3: Run full test suite**

Run: `make test`
Expected: PASS.

Run: `make lint`
Expected: PASS (or no new lint errors).

- [ ] **Step 4: Build with UI**

Run: `make build TAGS="http ui"`
Expected: binary produced at `build/coddy`.

- [ ] **Step 5: Manual smoke test**

1. Start `./build/coddy http` with a minimal `config.yaml`.
2. Open the UI.
3. Open Settings → Providers → add an OpenAI provider with a dummy key.
4. Open Settings → Models → add `openai/gpt-4o-mini`.
5. Close settings, verify the model appears in the Composer dropdown.
6. Start a chat (it will fail because key is dummy, but model selection should work).
7. Refresh browser, verify model list persists.

- [ ] **Step 6: Commit**

```bash
git add external/httpserver/openapi.go docs/http-api.md
git commit -m "docs: document admin config API and update OpenAPI spec"
```

---

## Self-Review Checklist

**1. Spec coverage:**
- Add provider via UI → Task 8, 10.
- Edit provider via UI → Task 8.
- Delete provider via UI → Task 8.
- Add model via UI → Task 9, 10.
- Edit model via UI → Task 9.
- Delete model via UI → Task 9.
- No manual `config.yaml` editing → entire runtime overlay approach.
- Model appears in Composer dropdown → Task 5 (handleModels) + Task 10 (refresh).
- Persistence across restarts → Task 1 (file I/O).

**2. Placeholder scan:**
- No TBD/TODO/fill-in-details found.

**3. Type consistency:**
- `AdminProvider` / `AdminModel` in UI match JSON shapes emitted by Go handlers.
- `model` ID format remains `provider/name` everywhere.
- URL parameter `{id}` for models will be URL-encoded by JS `encodeURIComponent` and decoded by Go `r.PathValue` (Go 1.22+ does not auto-decode path values; verify whether manual decode is needed in handlers).

**4. Gap:**
- If a provider is deleted, any runtime model referencing it is also deleted (handled in Task 3 Step 2). Static models referencing a deleted runtime provider are *not* deleted — this is acceptable because static config is read-only.
