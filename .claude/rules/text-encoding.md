---
description: Safe decoding of file text ingress
paths:
  - "internal/textenc/**/*.go"
  - "internal/platform/output_other.go"
  - "internal/platform/output_windows.go"
  - "internal/tools/fs/read*.go"
  - "internal/session/promptfiles.go"
  - "internal/session/mentions.go"
  - "internal/devinfake/**/*.go"
---
# Text encoding ingress

- File text ingress returns valid UTF-8 or a binary or undecodable error through `textenc.DecodeToUTF8`; never cast arbitrary bytes to text.
- Check valid UTF-8 before the NUL or binary gate, and detect BOM-less UTF-16 separately.
- Route known images and binaries through their dedicated path before provider text handling.
- Do not choose a charset only from a chardet name or confidence. Preserve the concentrated non-ASCII second pass and guarded system ANSI preference.
- Keep the Devin fake's invalid-UTF8 rejection.
- This policy excludes grep and command byte semantics.
- Heuristic changes require Windows tests, `make check-windows`, and `make lint-windows`.
