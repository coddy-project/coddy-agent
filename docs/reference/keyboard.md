# Keyboard

The keys of the console TUI and of the web UI composer, as the code binds them: the console table comes from `external/cli/app.go` and `external/cli/tui/editor.go`, the web UI table from `external/ui/src/ui/chat/Composer.tsx` and the components it opens. The console prints its own short version with `/hotkeys`.

## Console

Keys are parsed into the `ctrl+x` / `shift+enter` / `alt+backspace` notation of `external/cli/tui/keys.go`. While a modal is open, only the modal sees the keyboard; while the suggestion menu of the editor is open, it takes the navigation keys first.

| Key | Action |
|---|---|
| enter | send |
| shift+enter / ctrl+j | newline (a backslash right before enter also splits the line, for terminals that do not report shift+enter) |
| escape | close the suggestion menu if open; otherwise stop a running `!!` command; otherwise interrupt the running turn (`HandleSessionCancel`) |
| ctrl+c | clear the editor; on an empty editor, twice within 2 s exits |
| ctrl+d | exit when the editor is empty |
| ctrl+l | model selector |
| ctrl+p / ctrl+shift+p | cycle the configured models forward / backward |
| shift+tab | cycle the reasoning level (models with `reasoning_levels`) |
| ctrl+o | expand the header hints, the last tool output and the last `!!` block |
| ctrl+t | collapse or expand thinking blocks |
| up / down | prompt history on the first / last line of the draft; cursor movement otherwise |
| tab | open the suggestion menu for the word at the cursor; inserts a tab when there is nothing to suggest |
| `/` at the start of the draft, `@` before a path | open the command menu and the file mention menu as you type |

Editing keys inside the draft:

| Key | Action |
|---|---|
| left / ctrl+b, right / ctrl+f | one character |
| alt+left / ctrl+left / alt+b, alt+right / ctrl+right / alt+f | one word |
| home / ctrl+a, end / ctrl+e | start / end of the line |
| backspace, delete | one character back / forward |
| ctrl+w / alt+backspace | delete the word before the cursor |
| alt+d / alt+delete | delete the word after the cursor |
| ctrl+u, ctrl+k | delete to the start / end of the line |

Menus, selectors and prompts:

| Key | Where | Action |
|---|---|---|
| up / down | suggestion menu, every selector | move the highlight |
| enter / tab | suggestion menu | apply the highlighted row; enter on a slash command applies and submits in one stroke, so `/resume` + enter opens the picker directly |
| escape | suggestion menu | close it |
| typed characters, backspace | model, mode, theme and resume selectors | filter the list |
| enter | selector, permission prompt, question | choose the highlighted option (a permission prompt offers the options the agent sent) |
| escape / ctrl+c | selector, permission prompt, question | cancel; a cancelled permission prompt rejects the call |
| space | question with `multiple: true` | toggle the highlighted option |
| escape | custom answer editor of a question | back to the option list |

Platform notes from the code: a lone Escape is resolved after 10 ms locally and 100 ms when `SSH_CONNECTION` or `SSH_TTY` is set (`external/cli/tui/terminal.go`); the backslash-and-enter newline exists for terminals that do not report shift+enter; `ctrl+c` on a non-empty editor clears it, so the exit needs an empty editor and a second press.

## Web UI

The composer is a plain `textarea`; keys not listed here keep their browser meaning.

| Key | Where | Action |
|---|---|---|
| Enter | composer, desktop | send when idle: the draft, or the attachments alone when the selected model is multimodal; nothing while a turn is generating |
| Shift+Enter | composer, desktop | newline (browser default, not intercepted) |
| Enter | composer, mobile shell (viewports under 1200 px) | newline; sending is the button only |
| ArrowUp / ArrowDown | slash menu open | move the highlighted row, wrapping at both ends |
| Tab | slash menu open | apply the highlighted command |
| Tab | `@` file menu open | apply the first match |
| Enter | slash or `@` menu open | apply, without sending |
| Escape | slash, `@` or line-range picker open | close the picker; the `@path:N-M` picker stays closed for that mention until the draft moves on |
| Escape | context breakdown popover open | close it |
| Ctrl+Z / Cmd+Z | composer, right after Improve prompt | restore the draft from before the improvement, once |
| Enter / Space | context ring button focused | open or close the breakdown |
| Enter / Escape | model menu filter (shown with more than five backends) | pick the first match / close the menu |
| Escape | History sidebar, scheduler drawer, job editor | close; the job editor closes first, then the drawer |
| Escape | question prompt | skip the questions |
| Escape / Tab | confirmation dialog | cancel / keep the focus inside the dialog |

Stopping a turn has no key: it is the Stop button in the composer bar. The `@` menu opens as you type an `@` followed by a path, and a `:` after the path turns it into the line-range picker; neither is a binding.
