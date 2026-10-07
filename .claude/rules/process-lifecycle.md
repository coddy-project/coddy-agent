---
description: Cross-platform process identity, adoption, and termination
paths:
  - "internal/platform/**/*.go"
  - "internal/bgtask/**/*.go"
  - "internal/tools/shell/**/*.go"
  - "internal/hooks/**/*.go"
---
# Process lifecycle

- On Windows, process identity is PID plus creation time. Verify it while holding the handle through tree termination, and do not reap legacy rows without identity.
- Unix process-group liveness does not require the original leader.
- On Darwin, EPERM means an empty or zombie-only group only in the termination path, not in liveness checks.
- Every `exec.Cmd` goes through `platform.AdaptCommand` before `Start`.
- A foreground timeout adopts the same process into the existing pool; never restart it through a second scheduler.
- Background starts and adoptions cannot promise that daemon descendants close inherited pipes.
- Process-group changes require real macOS tests.
