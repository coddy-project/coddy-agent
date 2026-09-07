Feature: Line-range @mentions in the composer
  A composer mention can narrow a workspace file to a 1-based inclusive line
  range - "@Dockerfile:3-5". Only those lines are hydrated into the prompt, and
  the attachment the model sees records the range, so pointing at a fragment of
  a long file costs the turn only that fragment. Both entry points behave the
  same: an explicit attachment sent by the composer, and a ranged @path the user
  typed into the prompt text by hand.

  Scenario: An attached mention with a line range hydrates only those lines
    Given a workspace file "Dockerfile" with content:
      """
      FROM golang:1.23 AS build
      WORKDIR /src
      COPY go.mod go.sum ./
      RUN go mod download
      COPY . .
      RUN go build -o /out/coddy ./cmd/coddy
      FROM gcr.io/distroless/base
      COPY --from=build /out/coddy /coddy
      """
    When I attach "Dockerfile" lines 3 to 5 to the prompt "почему медленно @Dockerfile:3-5"
    Then the prompt has a resource for "Dockerfile#L3-5"
    And the resource text is:
      """
      COPY go.mod go.sum ./
      RUN go mod download
      COPY . .
      """
    And the model sees an attachment for "Dockerfile" with lines "3-5"

  Scenario: A ranged mention typed by hand hydrates the same lines
    Given a workspace file "app.go" with content:
      """
      package main

      func main() {
        println("hi")
      }
      """
    When I hydrate the prompt text "смотри @app.go:3-5"
    Then the prompt has a resource for "app.go#L3-5"
    And the resource text is:
      """
      func main() {
        println("hi")
      }
      """
    And the model sees an attachment for "app.go" with lines "3-5"

  Scenario: A mention without a range still hydrates the whole file
    Given a workspace file "notes.txt" with content:
      """
      first
      second
      third
      """
    When I hydrate the prompt text "прочитай @notes.txt"
    Then the prompt has a resource for "notes.txt"
    And the resource text is:
      """
      first
      second
      third
      """
    And the model sees an attachment for "notes.txt" without a line range
