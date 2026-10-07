---
description: Docker staging for symlinked SPA and documentation assets
paths:
  - "Dockerfile"
  - "external/ui/src/assets/**"
  - "external/ui/image_assets_test.go"
  - "docs/assets/**"
---
# UI asset staging

- SPA asset symlink targets under `docs/assets` must be staged at `/docs/assets` in the UI Docker stage.
- Every added or retargeted symlink requires a matching Docker `COPY` source pattern.
- Keep `TestDockerfileStagesEverySymlinkedAsset` green, and run `docker build --target ui-builder` when staging changes.
- Run `make docs` after asset consumers or patterns change.
