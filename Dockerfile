## Multi-stage build producing a minimal scratch runtime image.
## Stage 1 (Node) builds the SPA bundle synced into external/ui for go:embed when BUILD_TAGS contains ui.
## Stage 2 (Go) respects BUILD_TAGS (comma-separated, same as make / go build -tags).
## Both build stages run on the build platform and the Go stage cross-compiles
## for the target one, so a multi-arch build (`--platform linux/amd64,linux/arm64`)
## runs no step under emulation and every variant gets a binary of its own
## platform. scripts/check-image.sh (make check-image) holds that.

# The bundle is JavaScript, the same for every platform, so it is built once,
# natively, whatever platforms the image is built for.
FROM --platform=$BUILDPLATFORM node:22-bookworm AS ui-builder

WORKDIR /ui
COPY external/ui/package.json external/ui/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY external/ui/ ./
# The SPA's src/assets entries are symlinks into docs/assets, and COPY brings
# them over as links, so their targets have to exist at the same path inside
# the image or the bundler cannot resolve the imports. The glob covers every
# logo the SPA links today and the next one nobody remembers to add here.
COPY docs/assets/coddy-logo-*.svg docs/assets/favicon-32.png docs/assets/favicon.ico docs/assets/apple-touch-icon.png /docs/assets/
RUN npm run build:go


# The compiler runs natively too; GOOS and GOARCH below pick the target.
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build

# The official image sets GOTOOLCHAIN=local, which would build with whatever
# 1.26 release a cached copy of this image holds. auto lets go switch to the
# `toolchain` line of go.mod when the image is older, so the image never links
# an older standard library than CI and the release archives.
ENV GOTOOLCHAIN=auto

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
# Default build includes every surface, so one `coddy serve` can run whichever
# of them the mounted config.yaml enables
# by overriding CMD (see docker-compose command override). Pass --build-arg BUILD_TAGS
# to trim it. CI (docker-build-push.yaml) sets its own BUILD_TAGS for the published image.
ARG BUILD_TAGS=http,scheduler,ui,memory,gateway,cli,swarm
# BuildKit sets these from the platform being built. They are declared without
# a value on purpose: a default here wins over the value BuildKit passes, which
# is how the linux/arm64 image shipped an x86-64 binary (issue #482).
ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0
ENV GOOS=${TARGETOS}
ENV GOARCH=${TARGETARCH}
ENV VERSION=${VERSION}
ENV BUILD_TAGS=${BUILD_TAGS}

COPY --from=ui-builder /ui/index.html /ui/styles.css /ui/app.js /ui/events-worker.js /src/external/ui/
# The installable app: its manifest, icons and service worker (src/ui/pwa/).
COPY --from=ui-builder /ui/manifest.webmanifest /ui/sw.js /ui/icon-192.png /ui/icon-512.png /ui/icon-maskable-512.png /src/external/ui/
# The renderers app.js loads on demand (Mermaid, KaTeX), content-hashed.
COPY --from=ui-builder /ui/chunks /src/external/ui/chunks

RUN mkdir -p /out \
	/out/ssl-certs \
	&& GO_TAGS="$(printf '%s' "$BUILD_TAGS" | tr -d '[:space:]')" \
	&& if [ -n "$GO_TAGS" ]; then \
	go build \
	-tags="$GO_TAGS" \
	-trimpath \
	-ldflags "-s -w -X github.com/EvilFreelancer/coddy-agent/internal/version.Version=${VERSION}" \
	-o /out/coddy \
	./cmd/coddy/; \
	else \
	go build \
	-trimpath \
	-ldflags "-s -w -X github.com/EvilFreelancer/coddy-agent/internal/version.Version=${VERSION}" \
	-o /out/coddy \
	./cmd/coddy/; \
	fi \
	&& cp /etc/ssl/certs/ca-certificates.crt /out/ssl-certs/ca-certificates.crt

# The folders the unprivileged user of the runtime image writes: the home
# (CODDY_HOME and the default config file), the workspace and /tmp, which
# scratch does not have.
RUN mkdir -p /out/rootfs/home/user/.coddy /out/rootfs/workspace /out/rootfs/tmp \
	&& chmod 1777 /out/rootfs/tmp


FROM scratch

COPY --from=build /out/coddy /bin/coddy
COPY --from=build /out/ssl-certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=1000:1000 /out/rootfs/ /

# Coddy runs as an unprivileged user, uid and gid 1000, the usual first user of
# a Linux desktop, so the bind mounts of docker-compose.yml stay writable for
# the operator. Another owner is `--user UID:GID` (CODDY_UID / CODDY_GID in
# Compose); a home written by an earlier image that ran as root is chowned once
# (docs/getting-started/docker.md, The container user).
USER 1000:1000

WORKDIR /workspace

ENV CODDY_HOME=/home/user/.coddy
ENV CODDY_CWD=/workspace
ENV CODDY_CONFIG=/home/user/.coddy.yaml

EXPOSE 12345

# The same check docker-compose.yml runs: the binary starts. A scratch image has
# no shell or HTTP client to ask the server itself.
HEALTHCHECK --interval=30s --timeout=3s --retries=3 CMD ["/bin/coddy", "--version"]

ENTRYPOINT ["/bin/coddy"]
# Default subcommand. `serve` starts every subsystem config.yaml enables; the
# bind address is explicit because a container has to accept connections from
# outside itself, where the loopback default would leave the port unreachable.
CMD ["serve","-H","0.0.0.0","-P","12345"]
