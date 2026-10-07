---
description: Lossless and concurrent configuration round trips
paths:
  - "internal/config/**/*.go"
  - "internal/configapi/**/*.go"
  - "external/httpserver/config*.go"
  - "external/ui/src/ui/settings/**/*"
---
# Configuration round trips

- Use shared source loading for BOM and CRLF or CR normalization, and preserve the source line-ending convention on write.
- Relocate yaml.v3 syntax lines with bounded prefix parsing instead of trusting the raw parser line blindly.
- A PUT compares against the exact client revision and restores only untouched fields.
- When the source is missing, write one complete valid configuration; never restore defaults over the request.
- Restore write-only secrets from live state. Redaction placeholders must never overwrite stored values.
- Map fields use a three-way key merge for concurrent additions and removals.
- A successful save updates the SPA cache immediately. Stale reads cannot replace it; untouched forms may adopt refreshed copies, while edited forms wait for Reload.
