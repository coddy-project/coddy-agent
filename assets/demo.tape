# VHS tape — records coddy TUI demo GIF
Output assets/demo.gif

Require coddy

Set Shell bash
Set FontSize 16
Set Width 1600
Set Height 900
Set LineHeight 1.25
Set Theme "Catppuccin Mocha"
Set TypingSpeed 80ms
Set Padding 20
Set Framerate 30
Set CursorBlink false

# Open the TUI
Type "coddy"
Enter
Sleep 2s

# Show the interface loaded, then open command palette
Sleep 1s
Ctrl+P
Sleep 1.5s

# Close palette with Esc and switch mode to plan
Escape
Sleep 500ms
Tab
Sleep 1.5s

# Switch back to agent mode
Tab
Sleep 500ms

# Type a sample message and send it
Type "run tests and print result"
Sleep 1s
Enter

# Wait for the agent to finish (LLM + tools roundtrip can take a while)
Sleep 10s

# Disable input to enable viewport scrolling, then jump to the bottom
Ctrl+X
Sleep 500ms
Down
Sleep 3s

# Give the user time to read the agent response before exiting
Sleep 2s

# Exit — TUI prints the session name to stdout
Ctrl+C

# Wait for the session save message to be visible, then finish
Sleep 2s
