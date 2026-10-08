Feature: Every variant of the Docker image carries a coddy binary of its own platform
  The release builds the Dockerfile for linux/amd64 and linux/arm64 in one
  multi-platform push from an amd64 runner. Docker labels each variant with the
  platform it was asked for, whatever the binary inside is, so the Go stage has
  to compile coddy for the platform BuildKit is building: the linux/arm64
  variant once shipped an x86-64 binary because a default value on
  `ARG TARGETARCH` replaced the platform BuildKit passes (issue #482). The build
  stages run on the build platform and cross-compile, so no step of a
  multi-platform build runs under emulation.

  Scenario Outline: The Go stage compiles coddy for the platform being built
    Given the Dockerfile of the repository
    When BuildKit builds it for "<platform>" on "<build>"
    Then it compiles coddy for GOOS "<goos>" and GOARCH "<goarch>"
    And the stage that runs go build runs on "<build>"

    Examples:
      | platform    | build       | goos  | goarch |
      | linux/amd64 | linux/amd64 | linux | amd64  |
      | linux/arm64 | linux/amd64 | linux | arm64  |
      | linux/arm64 | linux/arm64 | linux | arm64  |
      | linux/amd64 | linux/arm64 | linux | amd64  |
