Feature: The Settings form saves itself, and asks for Save only where a change is a deliberate act
  Issue #485: a change in the web UI's Settings took effect only after Save, and nothing on
  screen said so. A provider signed in through ChatGPT was not usable until Save was pressed
  as well, because the sign-in felt like the step that applied it.

  The form now saves on its own a moment after the last edit, so several edits in a row go
  out as one save. A few changes wait for the Save button instead, because applying one by
  accident would cost something: a subsystem of coddy serve turned on or off (the Telegram
  and Pachca bots, the scheduler), the address a server binds, a relay's credentials and its
  links to other machines, and a provider or a model taken out. The schema the form is drawn
  from marks them. While any waits, the Save button stands out with the count of them and a
  list above it says what they are and that they take effect only after Save; Discard drops
  them. The rest of the form keeps saving itself in the meantime.

  Nothing is lost when the drawer goes away: a save waiting for the pause goes out at once,
  changes waiting for Save are there when the drawer opens again, closing it with them asks
  first, and so does leaving the page.

  Scenario: An ordinary change saves itself after a pause
    Then several edits of an ordinary field go out as one save a moment after the last of them

  Scenario: Turning a bot on waits for Save, which stands out until it is pressed
    Then turning a gateway on waits for the highlighted Save while the rest of the form saves itself

  Scenario: A provider taken out waits for Save, and Discard puts it back
    Then removing a provider waits for Save and Discard puts it back

  Scenario: Closing Settings with a change waiting for Save asks first
    Then closing the drawer with a change waiting for Save asks to save it or keep editing

  Scenario: A change waiting for Save outlives the drawer
    Then a change waiting for Save is there again when the drawer opens

  Scenario: A value put back after a save is saved too
    Then the next save goes out under the revision the last save answered with
