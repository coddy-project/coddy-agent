Feature: Identify chat messages with a screen reader
  Screen reader users can identify prompts and replies without synthetic
  headings disrupting the outline of Markdown content.

  @ui
  Scenario: Messages have accessible identities and preserve Markdown headings
    Then prompts and replies have localized article labels and preserve Markdown heading levels
