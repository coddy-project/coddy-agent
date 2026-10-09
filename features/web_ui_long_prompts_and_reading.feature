Feature: Long prompts, quotes and moving around a conversation in the web UI
  Issue #342. The composer's field follows its text and can be expanded over
  the chat for a long prompt, with a control under the jump to the newest
  message; a passage of the transcript can be quoted into the next message;
  and another conversation opens on its newest message.

  Scenario: The composer grows with a long prompt and expands over the chat
    Then the composer field grows with its text up to a ceiling and scrolls past it
    And the expand control gives the field the chat under its header, and a send folds it back
    And the expand control stands under the jump to the newest message, which an expanded composer hides, and another chat opens folded

  Scenario: Quote what the reader selected in the transcript
    Then selecting text in an answer offers Quote, which adds it to the draft as a quote after what is already there
    And a sent prompt shows its quotes as quotes

  Scenario: Another conversation opens on its newest message
    Then a conversation opened after the reader scrolled up in another one lands on its newest message
