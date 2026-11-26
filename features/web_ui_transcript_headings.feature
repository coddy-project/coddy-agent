Feature: Navigate chat messages by heading
  Screen reader users can move between prompts and replies using heading
  navigation without changing the visible transcript or its Markdown headings.

  @ui
  Scenario: A prompt and its reply each start with a heading
    Then a sent prompt and its agent reply each have a localized level-two heading before their content, visible only to assistive technology
