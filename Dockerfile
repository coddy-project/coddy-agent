## Multi-stage build producing a minimal scratch runtime image.
## Stage 1 (Node) builds the SPA bundle synced into external/ui for go:embed when BUILD_TAGS contains ui.
## Stage 2 (Go) respects BUILD_TAGS (comma-separated, same as make / go build -tags).

FROM node:22-bookworm AS ui-builder

WORKDIR /ui
COPY external/ui/package.json external/ui/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY external/ui/ ./
RUN npm run build:go


FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG BUILD_TAGS=http,scheduler,ui,memory
ARG TARGETOS=linux
ARG TARGETARCH=amd64

ENV CGO_ENABLED=0
ENV GOOS=${TARGETOS}
ENV GOARCH=${TARGETARCH}
ENV VERSION=${VERSION}
ENV BUILD_TAGS=${BUILD_TAGS}

COPY --from=ui-builder /ui/index.html /ui/styles.css /ui/app.js /src/external/ui/

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


FROM scratch

COPY --from=build /out/coddy /bin/coddy
COPY --from=build /out/ssl-certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

WORKDIR /workspace

ENV CODDY_HOME=/home/user/.coddy
ENV CODDY_CWD=/workspace
ENV CODDY_CONFIG=/home/user/.coddy.yaml

EXPOSE 12345

ENTRYPOINT ["/bin/coddy"]
CMD ["http","-H","0.0.0.0","-P","12345"]
