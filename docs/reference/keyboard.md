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
| F1 | the built-in documentation ([Built-in documentation](../features/built-in-docs.md#the-console-help)); `/docs` where the terminal keeps F1 for itself |
| ctrl+p / ctrl+shift+p | cycle the configured models forward / backward |
| shift+tab | cycle the reasoning level (models with `reasoning_levels`) |
| ctrl+o | expand the header hints, the last tool output and the last `!!` block |
| ctrl+t | collapse or expand thinking blocks |
| up / down | prompt history on the first / last line of the draft; cursor movement otherwise |
| tab | open the suggestion menu for the word at the cursor; inserts a tab when there is nothing to suggest |
| `/` at the start of the draft, `@` anywhere | open the command menu and the mention menu as you type ([Mentions](../features/mentions.md#in-the-console)) |
| tab / enter | mention menu open: take the highlighted row; a folder or `@session:` keeps the menu open on what it holds |
| escape | mention menu open: close it |

Inside the `/tasks` overlay ([Background tasks](../features/background-tasks.md#in-the-console)):

| Key | Action |
|---|---|
| up / down | move between the tasks |
| enter | open the task under the cursor: its command and the last lines of its output |
| s | stop the task under the cursor, or the open one, if it is still running |
| r | read the tasks, and the open task's output, again |
| escape | leave the open task; on the list, close the overlay |

Inside the help that F1 and `/docs` open ([Built-in documentation](../features/built-in-docs.md#the-console-help)):

| Key | Action |
|---|---|
| letters, backspace | edit the search (in the list) |
| up / down | move between the entries; on a page, scroll a line |
| enter | open the entry at its section; on a page, scroll a line |
| pgup / pgdn / space | scroll a page by a screen |
| home / end | the top / the bottom of the page |
| tab / shift+tab | the next / the previous section of the page |
| n / p | the next / the previous page of the documentation |
| / | from a page, back to the search |
| escape | leave the page; on the list, close the help |
| F1 / ctrl+c | close the help |

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
| Enter | composer, any device with a keyboard, a narrow window included | send when idle: the draft, or the attachments alone when the selected model is multimodal; while a turn runs, queue the draft for its next step |
| Shift+Enter | composer | newline (browser default, not intercepted) |
| Ctrl+Enter / Alt+Enter | composer | newline at the caret, replacing a selection (browsers insert none, so the composer does) |
| Cmd+Enter | composer | send, like Enter |
| Enter | composer, touch-only device (no hovering pointer, a coarse one: a phone) | newline; sending is the button, since a phone keyboard has no Shift+Enter |
| Enter | composer, while an input method is composing | confirms the candidate and never sends |
| ArrowUp / ArrowDown | slash or `@` menu open | move the highlighted row, wrapping at both ends |
| Tab | slash menu open | apply the highlighted command |
| Tab | `@` mention menu open | apply the highlighted row, also while a turn runs |
| Enter | slash or `@` menu open | apply, without sending; a folder or a scheme row keeps the `@` menu open |
| Escape | slash, `@` or line-range picker open | close the picker; the `@path:N-M` picker stays closed for that mention until the draft moves on |
| Escape | context breakdown popover open | close it |
| Ctrl+Z / Cmd+Z | composer, right after Improve prompt | restore the draft from before the improvement, once |
| Enter / Space | context ring button focused | open or close the breakdown |
| Enter / Escape | model menu filter (shown with more than five backends) | pick the first match / close the menu |
| Escape | History sidebar, scheduler drawer, job editor | close; the job editor closes first, then the drawer |
| Enter | question prompt | send the answer, once every question has one; typed in the composer it still belongs to the composer |
| Escape | question prompt | skip the questions |
| Escape / Tab | confirmation dialog | cancel / keep the focus inside the dialog |
| F1 | anywhere | open the documentation reader, or close it ([Built-in documentation](../features/built-in-docs.md#the-web-ui-reader)) |
| / | documentation reader, outside a field | put the cursor in its search box |
| ArrowUp / ArrowDown, Enter, Escape | documentation search box | move the selected hit, open it at its section, clear the search |

Stopping a turn has no key: it is the Stop button in the composer bar. The `@` menu opens as you type an `@` ([Mentions](../features/mentions.md#in-the-web-ui)), and a `:` after a file turns it into the line-range picker; neither is a binding.
