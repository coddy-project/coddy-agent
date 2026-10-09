package main

// What GOOS and GOARCH the Go stage of the Dockerfile compiles coddy for, read
// the way BuildKit resolves its arguments, so `make test` catches a Dockerfile
// that builds every variant of the image for one platform (issue #482) without
// Docker on the machine. scripts/check-image.sh (`make check-image`, the
// "Docker image" job of every pull request) builds the image for real and reads
// the ELF header of each binary; this is the check that needs nothing.
//
// The model holds what BuildKit was seen to do: an automatic platform argument
// (TARGETARCH and the rest) reaches a stage only through an ARG of that stage,
// a bare ARG takes the global value or else the automatic one, and a default
// value - on the global ARG or on the stage's - replaces the automatic value.

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// platformArgs are the arguments BuildKit sets from the platforms of a build.
var platformArgs = []string{
	"TARGETPLATFORM", "TARGETOS", "TARGETARCH", "TARGETVARIANT",
	"BUILDPLATFORM", "BUILDOS", "BUILDARCH", "BUILDVARIANT",
}

// dockerInstruction is one instruction of a Dockerfile, its continuation
// lines joined and its comment lines dropped.
type dockerInstruction struct {
	line    int // where it starts, 1-based
	keyword string
	args    string
}

func parseDockerfile(src string) []dockerInstruction {
	var out []dockerInstruction
	var cur strings.Builder
	start := 0
	for i, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if cur.Len() == 0 {
			start = i + 1
		} else {
			cur.WriteByte(' ')
		}
		if strings.HasSuffix(line, `\`) {
			cur.WriteString(strings.TrimSuffix(line, `\`))
			continue
		}
		cur.WriteString(line)
		keyword, args, _ := strings.Cut(cur.String(), " ")
		out = append(out, dockerInstruction{line: start, keyword: strings.ToUpper(keyword), args: strings.TrimSpace(args)})
		cur.Reset()
	}
	return out
}

// argDecl is one name of an ARG instruction and its default, if it has one.
type argDecl struct {
	name, value string
	hasValue    bool
}

func argDecls(args string) []argDecl {
	var out []argDecl
	for _, field := range strings.Fields(args) {
		name, value, ok := strings.Cut(field, "=")
		out = append(out, argDecl{name: name, value: unquote(value), hasValue: ok})
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// envPairs reads `ENV k=v k2=v2` and the legacy `ENV k v`.
func envPairs(args string) [][2]string {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return nil
	}
	if !strings.Contains(fields[0], "=") {
		key, value, _ := strings.Cut(args, " ")
		return [][2]string{{key, strings.TrimSpace(value)}}
	}
	var out [][2]string
	for _, field := range fields {
		key, value, _ := strings.Cut(field, "=")
		out = append(out, [2]string{key, unquote(value)})
	}
	return out
}

var dockerVarRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

func expandDockerVars(s string, vars map[string]string) string {
	return dockerVarRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := dockerVarRE.FindStringSubmatch(m)
		name := sub[1] + sub[4]
		if v := vars[name]; v != "" || sub[2] == "" {
			return v
		}
		return sub[3]
	})
}

// platformArgValues are the automatic arguments of a build for target on build.
func platformArgValues(target, build string) map[string]string {
	vars := map[string]string{}
	for prefix, platform := range map[string]string{"TARGET": target, "BUILD": build} {
		parts := strings.SplitN(platform, "/", 3)
		vars[prefix+"PLATFORM"] = platform
		vars[prefix+"OS"] = parts[0]
		if len(parts) > 1 {
			vars[prefix+"ARCH"] = parts[1]
		}
		if len(parts) > 2 {
			vars[prefix+"VARIANT"] = parts[2]
		}
	}
	return vars
}

// dockerRun is one RUN of the Dockerfile: the stage it is in and the platform
// that stage runs on, and for a RUN that runs `go build`, what the compiler
// targets.
type dockerRun struct {
	line          int
	stage         string
	stagePlatform string
	goBuild       bool
	goos, goarch  string
}

var goEnvAssignRE = regexp.MustCompile(`\b(GOOS|GOARCH)=("[^"]*"|'[^']*'|[^\s;&|]+)`)

// dockerRuns reads every RUN of the Dockerfile as BuildKit would run it when
// it builds the image for target on the build platform. A Dockerfile without
// `go build` is an error.
func dockerRuns(dockerfile, target, build string) ([]dockerRun, error) {
	autos := platformArgValues(target, build)
	global := map[string]string{} // the ARGs before the first FROM
	var (
		inStage              bool
		stage, stagePlatform string
		vars                 map[string]string
		out                  []dockerRun
		goBuilds             int
	)
	for _, in := range parseDockerfile(dockerfile) {
		switch in.keyword {
		case "FROM":
			fields := strings.Fields(in.args)
			inStage, stage, stagePlatform = true, "", target
			scope := map[string]string{}
			for k, v := range autos {
				scope[k] = v
			}
			for k, v := range global {
				scope[k] = v
			}
			for i, f := range fields {
				if p, ok := strings.CutPrefix(f, "--platform="); ok {
					stagePlatform = expandDockerVars(p, scope)
				}
				if strings.EqualFold(f, "AS") && i+1 < len(fields) {
					stage = fields[i+1]
				}
			}
			vars = map[string]string{}
		case "ARG":
			for _, d := range argDecls(in.args) {
				switch {
				case !inStage && d.hasValue:
					global[d.name] = expandDockerVars(d.value, global)
				case !inStage:
					global[d.name] = autos[d.name]
				case d.hasValue:
					vars[d.name] = expandDockerVars(d.value, vars)
				default:
					if v, ok := global[d.name]; ok {
						vars[d.name] = v
					} else {
						vars[d.name] = autos[d.name]
					}
				}
			}
		case "ENV":
			if !inStage {
				return nil, fmt.Errorf("line %d: ENV before the first FROM", in.line)
			}
			for _, kv := range envPairs(in.args) {
				vars[kv[0]] = expandDockerVars(kv[1], vars)
			}
		case "RUN":
			if !inStage {
				continue
			}
			if !strings.Contains(in.args, "go build") {
				out = append(out, dockerRun{line: in.line, stage: stage, stagePlatform: stagePlatform})
				continue
			}
			goBuilds++
			goos, goarch := vars["GOOS"], vars["GOARCH"]
			for _, m := range goEnvAssignRE.FindAllStringSubmatch(in.args, -1) {
				v := expandDockerVars(unquote(m[2]), vars)
				if m[1] == "GOOS" {
					goos = v
				} else {
					goarch = v
				}
			}
			// An empty GOOS or GOARCH is the compiler's own platform, the
			// platform the stage runs on.
			parts := strings.SplitN(stagePlatform, "/", 3)
			if goos == "" {
				goos = parts[0]
			}
			if goarch == "" && len(parts) > 1 {
				goarch = parts[1]
			}
			out = append(out, dockerRun{line: in.line, stage: stage, stagePlatform: stagePlatform, goBuild: true, goos: goos, goarch: goarch})
		}
	}
	if goBuilds == 0 {
		return nil, fmt.Errorf("no RUN of the Dockerfile runs go build")
	}
	return out, nil
}

// platformArgsWithDefaults lists the automatic platform arguments the
// Dockerfile declares with a default value, which replaces the one BuildKit
// passes.
func platformArgsWithDefaults(dockerfile string) []string {
	var out []string
	for _, in := range parseDockerfile(dockerfile) {
		if in.keyword != "ARG" {
			continue
		}
		for _, d := range argDecls(in.args) {
			if d.hasValue && slices.Contains(platformArgs, d.name) {
				out = append(out, fmt.Sprintf("line %d: ARG %s=%s", in.line, d.name, d.value))
			}
		}
	}
	return out
}

func TestDockerfileLeavesThePlatformArgumentsToBuildKit(t *testing.T) {
	dockerfile := readRepoFile(t, "../../Dockerfile")
	if found := platformArgsWithDefaults(dockerfile); len(found) > 0 {
		t.Errorf("the Dockerfile gives automatic platform arguments a default, which replaces the platform BuildKit builds for (issue #482):\n%s",
			strings.Join(found, "\n"))
	}
}

func TestDockerGoBuildTargets(t *testing.T) {
	const fixed = `
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0
ENV GOOS=${TARGETOS}
ENV GOARCH=${TARGETARCH}
RUN go build -o /out/coddy ./cmd/coddy
FROM scratch
COPY --from=build /out/coddy /bin/coddy
`
	cases := []struct {
		name, dockerfile, target, build  string
		wantPlatform, wantGOOS, wantArch string
	}{
		{
			name:         "cross-compiles on the build platform",
			dockerfile:   fixed,
			target:       "linux/arm64",
			build:        "linux/amd64",
			wantPlatform: "linux/amd64", wantGOOS: "linux", wantArch: "arm64",
		},
		{
			// The shape of issue #482: the stage default wins over BuildKit,
			// and the arm64 variant is compiled for amd64 under emulation.
			name: "a default on the stage's ARG replaces the target",
			dockerfile: `
FROM golang:1.26 AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ENV GOOS=${TARGETOS}
ENV GOARCH=${TARGETARCH}
RUN go build -o /out/coddy ./cmd/coddy
`,
			target:       "linux/arm64",
			build:        "linux/amd64",
			wantPlatform: "linux/arm64", wantGOOS: "linux", wantArch: "amd64",
		},
		{
			name: "a default on the global ARG replaces it too",
			dockerfile: `
ARG TARGETARCH=amd64
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS
ARG TARGETARCH
ENV GOOS=$TARGETOS GOARCH=$TARGETARCH
RUN go build ./cmd/coddy
`,
			target:       "linux/arm64",
			build:        "linux/amd64",
			wantPlatform: "linux/amd64", wantGOOS: "linux", wantArch: "amd64",
		},
		{
			// Running on the build platform makes GOARCH the only thing that
			// says which platform to compile for.
			name: "no GOARCH on the build platform compiles for the build platform",
			dockerfile: `
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETARCH
RUN go build ./cmd/coddy
`,
			target:       "linux/arm64",
			build:        "linux/amd64",
			wantPlatform: "linux/amd64", wantGOOS: "linux", wantArch: "amd64",
		},
		{
			name: "an argument the stage does not declare is empty there",
			dockerfile: `
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ENV GOARCH=${TARGETARCH}
RUN go build ./cmd/coddy
`,
			target:       "linux/arm64",
			build:        "linux/amd64",
			wantPlatform: "linux/amd64", wantGOOS: "linux", wantArch: "amd64",
		},
		{
			name: "an emulated stage without GOARCH compiles for the target",
			dockerfile: `
FROM golang:1.26 AS build
RUN go build ./cmd/coddy
`,
			target:       "linux/arm64",
			build:        "linux/amd64",
			wantPlatform: "linux/arm64", wantGOOS: "linux", wantArch: "arm64",
		},
		{
			name: "GOOS and GOARCH set on the RUN line",
			dockerfile: `
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH=${TARGETARCH} \
	go build -o /out/coddy ./cmd/coddy
`,
			target:       "linux/arm64",
			build:        "linux/amd64",
			wantPlatform: "linux/amd64", wantGOOS: "linux", wantArch: "arm64",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runs, err := dockerRuns(tc.dockerfile, tc.target, tc.build)
			if err != nil {
				t.Fatal(err)
			}
			var got []dockerRun
			for _, r := range runs {
				if r.goBuild {
					got = append(got, r)
				}
			}
			if len(got) != 1 {
				t.Fatalf("want one go build, got %+v", runs)
			}
			g := got[0]
			if g.stagePlatform != tc.wantPlatform || g.goos != tc.wantGOOS || g.goarch != tc.wantArch {
				t.Errorf("go build runs on %s for %s/%s, want on %s for %s/%s",
					g.stagePlatform, g.goos, g.goarch, tc.wantPlatform, tc.wantGOOS, tc.wantArch)
			}
		})
	}

	if _, err := dockerRuns("FROM node:22\nRUN npm ci\n", "linux/arm64", "linux/amd64"); err == nil {
		t.Error("a Dockerfile without go build must be an error, not a pass")
	}

	// A stage that only builds the web bundle still runs emulated when it
	// is not on the build platform, which the feature's last step reports.
	runs, err := dockerRuns(`
FROM node:22 AS ui
RUN npm ci
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETARCH
ENV GOARCH=$TARGETARCH
RUN go build ./cmd/coddy
`, "linux/arm64", "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].stage != "ui" || runs[0].stagePlatform != "linux/arm64" || runs[1].stagePlatform != "linux/amd64" {
		t.Errorf("want the ui RUN on linux/arm64 and the go build on linux/amd64, got %+v", runs)
	}
}

func TestPlatformArgsWithDefaults(t *testing.T) {
	src := "ARG BUILDPLATFORM=linux/amd64\nFROM x\nARG TARGETOS\nARG VERSION=dev TARGETARCH=amd64\n"
	got := platformArgsWithDefaults(src)
	want := []string{"line 1: ARG BUILDPLATFORM=linux/amd64", "line 4: ARG TARGETARCH=amd64"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
