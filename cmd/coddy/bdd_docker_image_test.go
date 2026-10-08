package main

// Godog harness for features/docker_image_platforms.feature: the Dockerfile of
// the repository read the way BuildKit resolves its platform arguments
// (dockerfile_platform_test.go), for each platform the release pushes. The
// image itself is built by scripts/check-image.sh (`make check-image`), which
// the "Docker image" job runs on every pull request.

import (
	"context"
	"fmt"
	"testing"

	"github.com/cucumber/godog"
)

type dockerImageState struct {
	dockerfile string
	runs       []dockerRun
}

func (s *dockerImageState) theDockerfile(t *testing.T) func() error {
	return func() error {
		s.dockerfile = readRepoFile(t, "../../Dockerfile")
		return nil
	}
}

func (s *dockerImageState) buildKitBuildsItFor(target, build string) error {
	runs, err := dockerRuns(s.dockerfile, target, build)
	if err != nil {
		return err
	}
	s.runs = runs
	return nil
}

func (s *dockerImageState) everyCommandRunsOn(platform string) error {
	for _, r := range s.runs {
		if r.stagePlatform != platform {
			return fmt.Errorf("line %d: the %q stage runs on %s, want %s (emulated, not on the build platform)",
				r.line, r.stage, r.stagePlatform, platform)
		}
	}
	return nil
}

func (s *dockerImageState) itCompilesFor(goos, goarch string) error {
	for _, g := range s.runs {
		if !g.goBuild {
			continue
		}
		if g.goos != goos || g.goarch != goarch {
			return fmt.Errorf("line %d: the %q stage compiles coddy for %s/%s, want %s/%s",
				g.line, g.stage, g.goos, g.goarch, goos, goarch)
		}
	}
	return nil
}

func TestDockerImagePlatformsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "docker-image-platforms",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &dockerImageState{}
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				*s = dockerImageState{}
				return ctx, nil
			})
			sc.Step(`^the Dockerfile of the repository$`, s.theDockerfile(t))
			sc.Step(`^BuildKit builds it for "([^"]*)" on "([^"]*)"$`, s.buildKitBuildsItFor)
			sc.Step(`^every command of the build runs on "([^"]*)"$`, s.everyCommandRunsOn)
			sc.Step(`^it compiles coddy for GOOS "([^"]*)" and GOARCH "([^"]*)"$`, s.itCompilesFor)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/docker_image_platforms.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("docker image platforms feature suite failed")
	}
}
