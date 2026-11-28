# UI design: layout

[Design index](../../DESIGN.md)

### Layout grid

The shell has four tiers, chosen by the viewport's width. Every width query of the stylesheet and every **`matchMedia`** in the code is an edge of a tier or a component threshold listed below, and the code reads the edges from **`shellBreakpoint.ts`** (**`PHONE_MAX_WIDTH_PX`**, **`SHELL_STACK_MAX_WIDTH_PX`**, **`WIDE_RAIL_MIN_WIDTH_PX`**) rather than spelling them out. **`layoutGridCss.test.ts`** reads the component table below as its allow-list: it fails on a width the stylesheet asks about that is neither a tier edge nor a row of that table in the direction the row gives, on range syntax or a unit other than px, on a row no rule uses any more, and on a **`matchMedia`** with a literal width outside **`shellBreakpoint.ts`**. A new width is a new row here first. The phone rules used to be split between **480px** and **520px** while the shell broke at **1199px**, and a phone from 521 to 599px got the tablet layout.

| | Phone | Tablet | Desktop | Wide |
| --- | --- | --- | --- | --- |
| Width | up to **`599px`** | **`600px`** to **`1199px`** | **`1200px`** to **`1919px`** | from **`1920px`** |
| Query | **`max-width: 599px`**, on top of the tablet query | **`max-width: 1199px`** (the stacked shell) | **`min-width: 1200px`** | **`min-width: 1920px`** |
| Devices | 360 to 430 in portrait: Galaxy S21, iPhone 13 mini, Honor X9d | iPad mini and Air in portrait (744 to 834), iPads in landscape (1024 to 1180), a narrow desktop window | laptops, desktop windows | large monitors |
| Navigation | top bar: **Coddy** over **agent**, the plus, **40px** icons, what does not fit folds behind **More** | top bar with the brand and every icon | vertical rail, icons only | the rail may widen to icons plus labels (**`coddy_nav_rail`**) |
| Scroll | the document; a nonempty chat outside the Mini App scrolls inside **`#messages`** | the document | one inner scrollport, **`#messages`** | as desktop |
| Composer | in normal flow at the foot of a nonempty chat outside the Mini App, otherwise docked to the viewport's bottom edge; the selector chips are one strip that scrolls sideways; the plate's picks are 36px tall, git's count slim with a 40px hit area | docked; the same plate on a touch screen | docked inside the chat column | as desktop |
| Menus and pickers | bottom sheets over a scrim | bottom sheets over a scrim | anchored dropdowns | as desktop |
| Transcript | one column, nothing wider than it | as phone | a centred **920px** stripe | as desktop |
| Settings | the drawer, **2**-wide section tiles whose names wrap to two lines, one section at a time behind **Back** | the drawer, section tiles, one section at a time | the section rail beside the form | as desktop |
| Drawers and panels | History, Scheduler, Tasks, Docs and Swarm take the screen between the insets | as phone | docked beside the rail | as desktop |
| Text fields | at least **16px** | at least **16px** on a touch-only device | as designed | as designed |

A phone is the stacked shell narrowed, not a fourth layout: everything the tablet column says holds on a phone unless the phone column says otherwise, and the rules live in one **`@media (max-width: 599px)`** block (see **Phone layout**). **599px** is the compact window class of the mobile platforms and the edge issue #340 asked for: every phone in portrait is under it, and the smallest tablet in portrait is over it.

**Navigation stays at the top on a phone.** The composer owns the bottom edge - inside a nonempty chat outside Telegram it stands in normal flow at the foot of a shell sized to the visual viewport, elsewhere docked and rising with the on-screen keyboard (**`interactive-widget=resizes-content`**); a bottom navigation bar would sit between the transcript and the composer, or under the keyboard. The top bar keeps **History** one tap away and folds the rest behind **More**, and the menus a phone opens are bottom sheets already.

Component thresholds decide a component's own layout from the width it gets, not the shell's tier. Each one is a row here:

| Threshold | Component | Why it is its own |
| --- | --- | --- |
| **`min-width: 640px`** | plan document card head | the title and the actions sit side by side only once the card is wide enough for both |
| **`max-width: 700px`** | swarm screen padding | the map needs the width more than the frame around it |
| **`min-width: 761px`** | desktop rail pill | the rail's track (**`--rail-pill-track`**) and its **44px** hit icons; it predates the grid and also reaches the tablet top bar from 761px |
| **`max-width: 1279px`** | documentation outline | the reader's third column (**On this page**) needs **1280px** |
| **`min-width: 900px`** | documentation outline on a tablet | on the stacked shell **On this page** stands beside the page once the page keeps at least about 600px next to it; below, it folds into a button above the page |

Verification widths: **360**, **375**, **393** and **430** for the phone, **599** and **600** for its edge, **834** for the tablet, **1199** and **1280** for the desktop's edge. **`npm run check:overflow`** (**`external/ui/scripts/phone-overflow-check.mjs`**, a live browser against the **`phone-overflow-check.html`** stand) measures the transcript at each of them.

### Phone layout

A phone gives the shell 360 to 430 CSS px. Everything below lives in one **`@media (max-width: 599px)`** block, the partial **`styles/shell/_phone.scss`**, which the entry uses after the stacked-shell rules it narrows, and nothing in it applies above 599px (the phone tier of the **Layout grid**). A few components narrow themselves under the same query next to their own rules: the Tasks control of the chat header, the permission card and the live status line.

- **The browser keeps the sizes the stylesheet sets.** **`html`** sets **`text-size-adjust: 100%`** and its **`-webkit-`** form. Without it iOS Safari enlarged the text of what it took for a wide block: the rows of a diff, written at **11.5px**, read at about **16px** on an iPhone while their line numbers stayed at **11px**. WebKit on a desktop has no such autosizing, so only an iPhone shows it.
- **Top rhythm.** On the stacked shell the top bar's pill stands **`--coddy-top-rhythm`** (**10px**) under the window's edge and the chat's title the same step under the pill (**`--coddy-mobile-title-sticky-top`**: the top inset less the bar's 4px of slack, plus the step); an expanded composer keeps the step under the title and around its control (see **Composer field height and expand**). One step, one variable: do not give either gap a number of its own. At rest, before the chat scrolls, the title stands **16px** under the pill (the stack gap and the bar's 4px of slack), and the first message keeps the same **16px** under the title (**`.chat-scroll-sticky-head`**), as on a desktop: a tighter gap below read as the title belonging to the transcript and apart from the menu.
- **Top bar.** The brand takes two lines, **Coddy** over **agent**, as in the narrow rail on a desktop, the **plus** (**`.rail-new-chat`**, **`nav-new-chat`**: a new chat, the accent's tint at rest, the rail's one action; under the brand in the narrow rail, and a labelled row like the others, above **History**, in the wide rail) right after it, and the brand is the element that gives way: **`.rail-brand`** and its wrapper **`.rail-brand-tip-host`**, which is the bar's flex item, may shrink and clip (**`flex: 0 1 auto; min-width: 0; overflow: hidden`**), **`.rail-middle`** grows but never shrinks (**`flex: 1 0 auto`**), so the icons keep to the right edge and are never painted over the brand. Icons are **40px** with a **4px** gap. The bar folds what it cannot hold behind a **More** button (three dots, **`nav-more`**, **`nav/navOverflow.ts`**): **History**, **Swarm** and the **environment** never fold, while **Settings** and **Extensions** always do, since they belong to the installation rather than to the session in front of the reader; the rest come back into the bar as room grows, in the order **Scheduler**, **Docs**, **Statistics**, **Sign out**. The menu under the button lists what is folded in the order **Extensions**, **Docs**, **Statistics**, **Scheduler**, **Settings**, then a separator and **Sign out** at the bottom. The menu hangs from the button's right edge (**`.rail-more-menu`**, opaque, **44px** rows, real **`href`** links), closes on a pick, **Escape** or a press outside, and the button lights up while a folded panel is open. **`NavRail`** measures the pill with a **`ResizeObserver`** only on the stacked shell; the desktop rail never folds. It is not a global app menu: it holds only what the bar has no room for, and nothing when everything fits.
- **Composer selector strip.** The bar scrolls its attach button and the mode, model, reasoning, service tier, permission and toolset chips inside **`.composer-tabs`**, one line under a **16px** fade at the right edge with a **12px** trailing spacer so the last chip can scroll clear of the fade, the context ring and **Send** fixed on the right. Chips keep their height (**8px** vertical padding); a long model name ends in an ellipsis (**11em**). Menus are portals and bottom sheets, so the scroller never clips them. The workspace is on the plate over the card, one line whose picks and count a finger can hit on a phone (**Workspace picks on the plate**).
- **A nonempty chat scrolls inside `#messages`, not the document.** Outside the Telegram Mini App a mounted chat marks the page **`html[data-phone-chat-scroll="inner"]`** (**`ChatScreen`**, while **`!isEmpty`**): **`html`** and **`body`** are locked (**`overflow: hidden`**, **`height: 100%`**, **`overscroll-behavior: none`**), the shell is sized to the bottom of the visible part of the window (**`--coddy-phone-chat-height`**, read off **`visualViewport`** on every **`resize`** / **`scroll`**, a transient or pinch-zoomed value skipped, **`100dvh`** until one lands), **`#messages`** becomes the scrollport (**`overflow-y: auto`**, **`overscroll-behavior-y: contain`**) with the chat's title sticking to its top, and the composer stands in normal flow at its foot (`.chat-bottom` turns **`position: static`**, the transcript keeps no reserve under it, **`--chat-composer-reserve`** is **0**). The on-screen keyboard then shortens the shell in every browser, iOS Safari included, so the newest message stays over the field; portalled sheets still read **`--coddy-keyboard-inset`**, which **`ChatScreen`** keeps setting. The start screen, a tablet and the Mini App keep the document scroll and the **`position: fixed`** dock.
- **Start screen.** **`.hero`** is one grid track that cannot grow past its container (**`minmax(0, 1fr)`**, at every width), so the page never scrolls sideways and the title stays centred.
- **Transcript.** On the stacked shell the transcript's column is the surface that scrolls - the page, or **`#messages`** inside a nonempty chat - so a row wider than the column scrolls it sideways; one tool row named after an MCP tool did it by 400px at 360px. Nothing in the transcript may be wider than it, at any tier: a tool row's label wraps inside the row when it is longer than the row (**Tool card UI**), and prose, links and inline code break where they have to (**Markdown**). Tables and code blocks keep their width and scroll in their own box. Tool rows stay folded until tapped. A numbered list's markers hang in the list's margin, so its width follows the longest number: **`Markdown`** sets **`--md-ol-digits`** from the list's last number and **`.md ol`** adds **`1ch`** per digit past the first; a fixed **18px** let `220.` stick out of the text column on the left.
- **Settings.** The section tiles keep two columns, and a section's name wraps to two lines instead of ending in an ellipsis (**`.settings-tile-title`**): a phone has no hover, so the full name in the tile's **`title`** tooltip is out of reach. An MCP server row puts its name and command line on the first line and wraps its controls to the row's end below them (**`.mcp-list-item-head`** **`flex-wrap: wrap; justify-content: flex-end`**, the text block **`flex-basis: calc(100% - var(--mcp-row-inset))`**), so a server name is read whole instead of a few letters between the status dot and four 40px buttons. A map row of the settings (the default headers of **HTTP requests**) puts the value under the name, the trash beside the pair (**`.settings-map-entry`** one column), and its rows stand 16px apart (**`.settings-map`**), twice the gap inside a pair, so a header name is read whole.
- **Text fields** are at least **16px** on a phone and on any touch-only device (**`(any-hover: none) and (any-pointer: coarse)`**), because iOS Safari zooms into a smaller field on focus and stays zoomed; the composer textarea and its highlight mirror change together.
- **Keyboard.** What Enter does follows the input device, not this width: see **Composer primary action**.

### Sessions drawer placement (implementation contract)

- **Horizontal alignment**: The **left edge** of the drawer is **`rail-column` right edge + gutter** (~**`--nav-floating-gutter`**). Do **not** hardcode **`left`** in **`px`** for "wide navbar" guesses (wide **`fit-content`** width varies).
- **Measured track width**: SPA sets **`--rail-shell-track-width`** on **`.shell`** to **`rail-column.offsetWidth`** (ResizeObserver in **`NavRail`**) before computing drawer **`left`** and **`width`** so narrow and labeled-wide rails stay flush with **`--nav-floating-gutter`** after the nav column.
- **CSS fallback**: When the variable is not yet set inline, **`--rail-shell-track-width`** defaults on **`.shell`** to **`calc(var(--rail-pill-track) + var(--rail-column-pad-end))`**.

### History grouping, tags and the archive

- **One control, not a row of them.** A sliders button (**`.sessions-filter-trigger`**) sits at the right
  end of the **search row**, and everything that decides what the list shows lives in the menu it opens
  (**`.sessions-filter-menu`**): Status, Environment, Group by, Sort by - status first, because "am I
  looking at the archive" is the question asked most often. It belongs with the search because both
  narrow the list below, and not in the head, where its neighbour would be a close button that does
  something else entirely. It takes the **height and corner radius of the search field** beside it
  (36px, 12px) so the row reads as one strip of controls. The menu hangs 6px under the trigger, its
  right edge on the trigger's (see **Portaled, not nested**).
- **Four rows deep, not four lists long.** Each row (**`.sessions-filter-row`**) carries its question on
  the left, the **answer in force** in the accent colour (**`.sessions-filter-value`**) and a chevron;
  the choices open beside it as **`.sessions-filter-submenu`**, on a mouse hover or on a click or tap,
  one at a time. Only a mouse opens a row by hovering it: a touch opens it by its tap, because iOS
  drops the click of a tap whose emulated hover shows new content. The click that follows the mouse
  onto a row it has just opened keeps the row open rather than folding it straight back; a later
  click, or Enter or Space on the focused row, folds it.
  A hairline above **`.starts-group`** separates the two rows that decide *what is listed* from the two
  that decide *how it is arranged* - a rule rather than headings, because all four are questions of the
  same kind. A section with only one row to choose from is not rendered at all.
- **Portaled, not nested.** The drawer is **`overflow: hidden`**, so a submenu inside it would be cut at
  its edge: the menu is rendered into the document (**`createPortal`**) and placed **`position: fixed`**
  from the trigger's rectangle, the same way the composer's menus are. The menu is never wider than the
  room left of its trigger (**`max-width`** from the trigger's right edge less 8px).
- **A list opens where its whole width fits.** A list's width depends on the locale and on the names of
  the remotes, so it is measured in a layout pass before it is painted (**`placeFilterSubmenu`**) and
  opened beside the menu on the right (the drawer sits at the left of the window), on the left near the
  window's right edge (**`.opens-left`**), each 8px clear of the window's edge. Where neither side has
  the room - a phone, where the drawer is the width of the screen and the trigger sits at its right
  edge - the choices **fold out under their row**, inside the menu (**`.opens-inline`**): indented under
  the question, no panel of their own, the row's chevron turned down as a disclosure's is. The menu then
  scrolls as a whole instead of running past the bottom of the window (**`.has-inline-submenu`**), and
  until it closes a click, not a hover, switches sections, since a list folding away under the pointer
  would move the rows beneath it. In a 393px window a list used to start at -100px, leaving only the
  check marks on the screen (issue #460). Pinned by **`SessionsFilterMenu.test.tsx`** and
  **`features/web_ui_menus.feature`**.
- **Only a value moved off its default is coloured.** The answer on a row is the accent colour when it
  differs from the default (**Active**, the first environment, **Folder**, **Last activity**) and a
  muted 42% text otherwise (**`.sessions-filter-value.is-default`**): a menu where every row is accented
  says nothing about what has been narrowed. Each of the four is remembered in its own cookie
  (**`sessionPrefs.ts`**), and every read validates what it finds, so a cookie from an older build
  cannot put the drawer into a state the code no longer has.
- **The menu is opaque.** It uses the **tooltip** surface (**`--coddy-tip-bg`** / **`--coddy-tip-shadow`**),
  not the glass panel: it sits directly over the list it filters, and a translucent panel there is read
  through. Both panels use it, so the row and its choices read as one surface.
- **Escape undoes one step**: an open submenu folds first, the menu closes second.
- **Group heading** (**`.session-group-head`**) is the bucket's **own name with the shared chevron after
  it** (**`<Chevron open />`**, see **Chevron**: pointing right while collapsed, turned down when open),
  not a band across the list, and it is **`flex: 1 1 auto`**: all of the row up to the **`+`** folds it,
  because a finger aims at a row and not at a word. **12.5px**, weight **600**, muted until hover, and in the **case the name already has** -
  upper-casing a folder path is a small lie. The label is translated for a fixed bucket (dates,
  *No workspace*, *No tags*) and is the operator's own text for a folder name or a tag.
- **The end of the bar is the `+`** (**`.session-group-new`**), an **18px** icon in a **34x32px** box whose
  centre stands in the **same column as the rows' own ⋮**. It is **visible at
  rest**, not revealed on hover: a way to start work in this folder is worth a glance, not a hunt with
  the pointer. There is no row count - the rows are right there to be looked at.
- **Hover brightens text, never a plate.** Both the heading and the **`+`** transition their **colour**
  only (140ms): a band appearing under the pointer makes a heading read like a row that can be opened.
- **A folder name more than one heading carries** gets the full path under it
  (**`.session-group-path`**, 10.5px, 40% text): the name alone cannot tell two checkouts apart, and
  that is the only case the line appears in. It is clipped plainly at the end - reversing the direction
  to keep the tail moves the leading slash to the other side, and a path that reads as ending in
  **`/`** is a worse lie than a truncated one. **`SessionGroup.subLabel`** carries it, set by
  **`groupSessions`** only for the names that repeat.
- **Buckets** that hold nothing are not drawn. Collapse state lives in the drawer, keyed by group, so a
  heading that comes and goes with a search keeps its state while it is on screen.
- **A folder heading carries a `+`** (**`.session-group-new`**) that starts a new chat in that workspace. Only a heading that *stands for a
  folder* has one - a date bucket is not a place to put a session - which is why **`SessionGroup`**
  carries **`workspacePath`** (the full path) next to the name it shows.
- **The pinned group** (**`.session-group.is-pinned`**) leads every mode, headed *Pinned* and closed by a
  hairline: the pins are one list kept by hand, and the groups below are the ones the machine made.
  **`groupSessions`** lifts the pinned rows out first, so a pin is held **once** - a conversation at the
  top *and* inside its folder would leave dragging one of the two meaning nothing.
- **A pinned row is dragged by the row itself** (**`.is-reorderable`**), with no grip: a handle beside
  the title pushed a pin's title off the edge every other row shares. A mouse or a pen starts the drag
  once the press has travelled **4px**, and the click that release produces is swallowed so the moved
  row does not open; a finger has to **hold the row for 400ms** first, so a swipe still scrolls the
  list, and once held the page's **`touchmove`** is cancelled so the finger moves the pin, not the
  list. The row takes no text selection, native link drag or touch callout. The drag runs on
  **pointer events** - HTML5 drag-and-drop never starts from touch - the dragged row fades
  (**`.is-dragging`**) and the **landing place** is drawn on the list itself - an inset accent rule
  along the top of the row it would take (**`.is-drop-target`**), which the 14px corner radius carries
  around the corners so it reads as that row being singled out - rather than a row following the
  pointer, which would survive no scroll and cost a compositing layer. The arithmetic is
  **`reorderPins`** / **`pinDropIndex`**, kept pure.
- **Archiving moves the row at once** and sends the **`PATCH`** behind it, and the list is **not read
  again**: a re-read from the first page dropped the rows scrolling had loaded, and with them the place
  in the list where the next conversation to archive sat. A refused **`PATCH`** puts the row back
  where it stood - before the row that followed it, or at its old index when that row has gone; if
  the operator switched the **Status** filter meanwhile, the row stays out and the list error says it
  instead - and says so **on the row** (**`.session-row-error`**, **10.5px** in the failure colour of a
  tool row's marker, **`role="alert"`**): the list error at the top of History is out of view once the
  list has been scrolled. The note is a sibling of the row's link, not part of it, so the link keeps
  the conversation's name; the row wraps to give it a line of its own (**`.has-row-error`**). It goes
  when the operator tries again or closes the drawer. Only the flag is put back: a title or tags that
  changed while the request was out stay. A listing issued before the **`PATCH`** settled still carries the row as it was, so every
  listing is shown with this tab's own moves applied until one issued after the settle comes back; and
  since the listing pages by an **offset**, each archive that took a loaded row out of the working list
  moves the next page's cursor back by one, or the conversation that moved up into the gap would fall
  between two pages. A page asked for while such an archive is still in flight starts one row earlier
  per archive, because the server may apply the **`PATCH`** before it answers the page; the price is at
  most a row fetched twice, which the merge drops by id. A listing that comes back after the list was
  read again from the top is dropped: it belongs to the list that read replaced (another filter,
  another order), and a page appended from it mixed rows of another filter into the new one. A page
  whose request fails lowers the loading flag, so History asks for it again (**`sessions/archiveMoves.ts`**,
  **`App.archiveSession.test.tsx`**).
- **An archived row is dimmed** (**`.session-item.is-archived`**): its title drops to 45% text and its
  tags to 60% opacity. Put aside and still in play differ by exactly that. The archive **mark**
  (**`.session-archived-mark`**) leads the row beside its state dot, where states
  belong - it is not a chip among the tags, which are labels the operator chose.
- **A running turn is a pulsing dot** (**`.session-activity-dot`**, **`sessionRowShowsActivity`**), not a
  spinner: the **unread dot** a finished background turn leaves behind (8px, the theme's accent) made **a third
  darker** (**`color-mix(in srgb, <unread colour> 67%, #000)`**) and pulsing between full and 30%
  opacity every 1.2s, held still under **`prefers-reduced-motion`**. The two marks are one family -
  *working* and *done, not read yet* - where a spinner beside them read as a third kind of state.
  **The open conversation carries it too**: it is the turn the reader is most likely waiting on, and a
  row without the mark reads as finished. Its row follows this tab's own view of the turn
  (**`generating`** in **`App.tsx`**) rather than the last listing, which is only refreshed on a poll.
  A row always reserves the same **24px** state-mark track: its 16px marker slot starts at the left edge,
  leaving the remaining space between marker and text. An idle session's transparent neutral
  **`.session-idle-dot`** ring keeps a finished row distinct from a group heading while the larger permission
  and question marks occupy the same track without moving its title or tags. A real failed
  turn carries **`lastErrorSeq`** in the session activity data: while its generation is newer than the
  read cursor it is a filled red **`.session-error-dot`**; opening that session advances the cursor and
  turns the mark into a red outline (**`.is-seen`**) until a later successful turn clears it. An unseen
  error wins over the generic accent unread-completion dot. A permission or question waiting on the
  reader owns the slot instead, so it renders no idle or error ring behind its own mark. A question marker stays still: the turn is waiting for a person rather than making progress.
- **The composer's slot on an archived conversation** is **`.archived-session-notice`**, cut from the
  same glass panel as the subagent notice beside it (**`--coddy-glass-panel-bg`** plus the backdrop
  filter) - it stands over the transcript, and a wash of the text colour is transparent on a dark
  canvas. A line of text, and one button that takes the conversation back out.
- **Row tags** (**`.session-row-tags`**) go **under** the title, not beside it: the title is what the row
  is for and must not be pushed out of view by labels. Chips are **10px**, pill-shaped, on a 6% text
  wash. **The first chip starts where the title's text starts**, never under the state marks: the link
  (**`.session-row-link`**) is a two-column grid, **`auto minmax(0, 1fr)`**, the marks
  (**`.session-row-marks`**, rendered for every row) hold the first column and keep their own 6px to
  the title, and the title line (**`.session-row-leading`**) and the tags share the second. Neither
  carries a nudge of its own; the idle ring makes that alignment stable in every grouping mode, covered
  by **`sessionRowTagsAlignCss.test.ts`**. The **archived badge** (**`.session-archived-badge`**) is the same size and wash but uppercase,
  and sits **inline after the title**, because it qualifies the title rather than the row.
- **A chat being named shimmers instead of showing its first message** (issue #435). From the first send
  of a new chat until its **`POST /coddy/describe`** settles (a name, an empty answer, a failure, or
  **`NAMING_PLACEHOLDER_TIMEOUT_MS`**, 30 seconds, in **`sessionTitleSuggest.ts`**, after which a late
  name is still applied but no longer promised), **`App.tsx`** holds the session
  in **`namingSessionIds`**: the chat header draws a bar where the title will be
  (**`.chat-title-skeleton`**, **`data-testid="chat-title-pending"`**, the button **`aria-busy`** and
  named *Naming the chat…* for screen readers) and the row draws one for the title
  (**`.session-title--pending`** / **`.session-title-skeleton`**) and two short ones where the tags will be
  (**`.session-row-tags--pending`** / **`.session-tag-skeleton`**). All of them are
  **`.naming-skeleton-bar`**: the settings skeleton's shimmer tinted from **`--text`**, so it reads on
  every theme, sized in **em** so it follows the type it stands in for, and still under
  **`prefers-reduced-motion`**. A first send of attachments alone has no text to name the chat by and
  puts up no placeholder. Never draw the first message there while the name is on its way: for a
  chat started with a command it is a **`/rpa-init`** token, and for a long prompt a sentence that is
  replaced a second later. The name and the tags of the same answer replace the bars in one frame.
- **One control per row** (**`.session-row-menu-trigger`**, a 26px **⋮**, 0.38 opacity until the row is
  hovered) opens **`.session-row-menu`**: **pin**, **rename** and **tags** - the three that change where
  the row sits or what it says about itself - then a hairline (**`.starts-group`**), then
  **archive** and **delete** together - both take the conversation out of the list, while pinning only
  moves it - with delete in the destructive colour. An icon per action cost the title a button's width each and made a
  mis-click a delete; inside the menu the actions have room for their words. The menu is portaled and
  placed from the trigger, and flips above the row near the foot of the window.
- **A pinned row carries no mark of its own**: it stands under the *Pinned* heading, which already says
  it, and a pushpin beside the title only took the title's room.
- **Rename edits the row in place** (**`.session-title-input`**, the box the chat header already uses):
  the name is **selected** when it opens, so typing replaces it and the box shows the beginning rather
  than the tail a caret at the end would scroll to. **Enter** and blur save, **Escape** leaves the title
  alone, and a name that did not move costs no request. The rename ends **once**: the key that ended
  it and the blur of the disappearing box both go through the same one-shot commit, so Escape cannot
  be followed by a blur that saves the discarded draft.
- **The tag editor** (**`.session-tag-editor`**, **`SessionTagEditor.tsx`**) is **one component for both
  places that show tags** - the row menu here and the **+** of the session table - because they are the
  same three gestures: drop one, type one, take one the history already uses. It is cut from the
  **tooltip** surface, like the row menu it opens from and for the same reason: it stands over the list.
  A chip carries its own cross (**`.session-tag-chip`** + **`.session-tag-chip-remove`**); the box
  (**`.session-tag-input`**) offers the labels this history already files under
  (**`.session-tag-suggest`**, most used first, the row's own excluded, prefix matches leading);
  **Enter** files what was typed, the arrow keys take over the list and **Enter** then files the
  highlighted one, **Backspace** on an empty box drops the last chip, **Escape** closes.
  **There is no Save**: every change is a **`PATCH`** at once. The list takes the new set **before**
  the request answers - the editor builds each gesture on the row it is shown, and a second gesture
  made while the first is in flight would otherwise start from the set before both - and the folded
  set the server answers with then replaces it, so a chip never changes spelling one refresh later.
  A refused write puts back what the row carried. The folded form of what is being
  typed is shown under the box (**`.session-tag-hint`**) **only when it differs** from what was typed -
  the arithmetic and the fold are **`tagEditing.ts`**, kept pure and a twin of `NormalizeTag` in Go.

### Session table: sorting, the archive and tags

- **The tags of a row end in a `+`** (**`.sessions-manager-tag-add`**, dashed, **visible at rest**): the
  chips themselves stay filters, as they were, and the **+** opens the same editor the row menu in
  History opens. A row you can file is worth a glance, and a finger has no hover to reveal it with.

- **Sortable column head** is a **`button`** inside the **`th`** (**`.sessions-manager-sort`**),
  carrying the caret (**▲** / **▼**, 0.6rem, 75% opacity) only while it is the sorted column; the
  **`th`** carries **`aria-sort`**. The header keeps the column's own typography - a sortable column
  must not look like a control bolted onto the table.
- **Toolbar order** is search, archive **`select`**, then the two icon actions: empty the **archive**
  (archive box), then delete the **ticked** rows (trash with a check). The destructive pair sits
  together at the trailing edge at every width.
- **Archived badge** (**`.sessions-manager-badge-archived`**) is the **neutral twin** of the accent
  **open** badge: a text-wash pill, because it says where a row sits, not that anything is wrong.
- **Tag chip** (**`.sessions-manager-tag`**) is a **button**: pressing one filters the table. An active
  tag filter is a muted line under the toolbar with an underlined accent link that clears it.
- **No per-row delete.** The row's only destructive surface is its tick; the scope of a delete is
  always what the operator can see ticked, or the archive scope named on its own button.

### Narrow-rail hover tooltips

- Shown **only** when the rail is **narrow** (no wide labels column). Labels visible in wide rail substitute for tooltips; do not show floating tip rows there.
- **Copy**: the plus beside the brand **New Chat** (the brand itself has no tooltip), **History** nav control **History**, **Scheduler** nav control **Scheduler**, external links match their labels. External accent reference (from another product, colours only) in **`docs/assets/ref-navbar-narrow-tooltips-accent.png`**.
- **While a control owns an open overlay** (example **History** with the list visible and **`.is-active`** on the trigger), **hide that row's tooltip** even if the mouse still hovers (**nav stacking can sit above backdrop**). Same for **Scheduler** when its drawer is open.
- Tooltip **horizontal offset** must use the **same gutter math** as the History drawer (**column padding + nav floating gutter (+ border shim where needed)**), not a shorter offset from icon-only **`rail-tip-host`** width alone.

### Nav rail panel and wide layout (design contract)

- **Panel shape (desktop)**. The nav is a **tall rectangular column** along the **left viewport edge** with **rounding only on the right** (straight left edge flush with the browser). Avoid a centered **full-height capsule** that does not meet the edge.
- **Wide rail width**. Pill width is **content-driven** (**`fit-content`**) with a sensible **max-width** cap, not a legacy fixed pixel width guess.
- **Wide header brand**. **Coddy agent** is **one horizontal line** (**Coddy** + muted **agent**). Keep **breathing room to the right** of the label (**extra padding-right** on the brand control) so copy does not sit against the inner right edge of the panel.
- **Labeled rows (History, Scheduler)**. Rows **share the same width** within the column (stretch to the **widest** row). Each row is a **single interactive surface** (icon + label), not a small icon hit target plus detached text.
- **Icon column alignment**. The first grid track for row icons matches the **collapse** toggle footprint (**44px** wide control). **Horizontal padding** on row hits stays **balanced** (avoid oversized **padding-left** and cramped **padding-right** at the label end).
- **Collapse vs global menu**. The **stacked-lines** control **only** narrows the rail. It is **not** a global app navigation drawer (see Nav rail item 2 above).

### Active counts on the rail

- **Over the icon, in every shape of the rail.** The count of **History** and **Scheduler** (**`.rail-active-count`**, a **14px** accent pill) hangs off the top right corner of the item's icon (**`top: -11px; right: -11px`**), and the icon and its count are one wrapper (**`.rail-nav-icon`**, **`position: relative`**, centred in the wide rail's 44px icon track). That holds icon-only, labelled (wide) and in the rows of the phone's **More** menu. The count is **never** placed from the row: the wide rail's row is as wide as its label, and a count placed from it ran to the far end of the label; the More menu's stood after the text. Do not give a count a place of its own beside a label again. Held by **`nav/railActiveCountCss.test.ts`** and **`NavRail.test.tsx`**.
- **What each counts.** **History** counts the active turns of chats and of their subagents (**`active_count`** of **`GET /coddy/sessions`**); a **scheduler run** is the scheduler's work and is counted on **Scheduler** alone (**`runs_active`** of **`GET /coddy/scheduler/jobs`**): the listing does not descend into a scheduler job session, where the runs live as child sessions. The Scheduler's count is read when the page opens and again at every **`turn_started`** / **`turn_ended`**, whether or not the Scheduler is open, and only the latest read is kept, so a run that ended does not stay on the rail until a reload. Held by **`App.schedulerRailCount.test.tsx`** and **`TestCoddySessionsListLeavesSchedulerRunsOutOfActiveCount`**.

### Nav rail icons (implementation contract)

- **Collapse (hamburger glyph)**. Use **three equal-length** horizontal lines (**no** shorter third line). Prefer a **compact symmetric** **`viewBox`** (for example **20×20**), **round** line caps, and stroke weight that reads at **18px** output size.
- **Expand (narrow rail at XL)**. Keep a **chevron / chevron-pair** style control that reads as **widen rail**, not a second burger menu.
- **Rendering**. Small rail SVGs should opt into **`shape-rendering` tuned for crisp curves** (for example **`geometricPrecision`**) and **`flex-shrink: 0`** so flex layout does not squash glyphs.
- **Regression art**. Use hover captures under **`docs/assets/`** when checking outline and press states.

### Escape and the screens of the rail

Every item of the rail but **Sign out** and the environment (which opens a menu, **Environment item and menu**) opens a **screen** over the chat: **History**, **Scheduler**, **Swarm**, **Docs**, **Settings**, and **Statistics**. **Escape** closes the screen on top the way its **×** does - the same function, so the address goes back to what was under the screen and a reload does not bring it back. The swarm screen over a chat has no ×: Escape closes it as its backdrop does. On a relay the swarm map is the home screen, with nothing under it, and Escape leaves it.

- **One rule for every screen, a future one included.** The rule lives in **`nav/railEscape.ts`**; **`App.tsx`** calls **`useRailScreenEscape`** once, with a table of every screen: whether it is open and what closes it. The table is a **`Record<RailScreenId, RailScreen>`**, **`RailScreenId`** being every **`NavItemId`** of the rail but **`signOut`**, so a screen added to the rail does not pass the type check (**`tsc`**, run by **`make lint`**, the pre-commit hook and CI; **`vite build`** does not check types) until it has its entry, its place in the stacking order (**`RAIL_SCREEN_DEPTH`**, which only settles a tie: the rail keeps one screen open) and its row in the page map of **`App.railEscape.test.tsx`**, whose test then opens it and closes it with Escape. Do **not** give a screen an Escape handler of its own: while every screen had to remember one, History and the scheduler had theirs and Docs and Settings had none.
- **Escape undoes one step.** What is nearer the key answers first and **claims** it (**`preventDefault`**, or stops the event): an open menu (the ⋮ of a History row, the History filter menu, **More** on a phone, the composer's **Mode** / **Model** / **Reasoning** / **Permission** menus, the environment, folder and branch menus), a picker or the context popover of the composer, the folder browser (its new-folder row first), the chat title being edited, the **(i)** tip of a settings field, the open list of a settings combobox, the marketplace results under the Skills install search, the documentation search with text in it (Escape clears it; in an empty box the key goes on and the reader closes), the settings search with results showing or with a query in its field (Escape clears the query; the next one is the drawer's), a confirmation dialog, the image viewer. A screen takes only an Escape nothing claimed. A menu, a popover or a dialog answers Escape through **`useEscapeCloses(open, close)`** (**`components/useEscapeCloses.ts`**), whose **`close`** may take a step of the layer's own first; a control that answers only while it has the focus claims the key in its own **`onKeyDown`**. A screen with a step of its own takes that step before it closes - the step the arrow of its head takes: the scheduler's open job or its runs go back to the job list, an open row of Settings goes back to its list, a Settings section on the stacked shell goes back to the tiles. Settings has one step before those, and the head's arrow does not take it: with the **results of the settings search** showing, Escape clears the query wherever the focus is (the head then reads **Settings** with no arrow, and Telegram's Back, which dispatches an Escape at the focused element, takes the same step); it is the first step of the screen's chain, registered with **`useRailEscapeStep`** like the others. A step of state **`App`** owns is the table entry's **`close`** while it applies (the scheduler); a step of state the screen keeps is registered from the screen with **`useRailEscapeStep(<screen>, step)`**, **`null`** while there is none (Settings).
- **Where it listens, and why there.** One **`keydown`** listener on **`document`**, bubble phase. That is after the controls of the page (React answers at its root, below **`document`**) and after the tips, menus and dialogs that listen in the capture phase, and before the listeners on **`window`**, which belong to the chat under the screen: the question card's Escape skips its questions. A step taken is claimed and stopped, so an Escape that closed a screen never reaches the chat under it, and the question card also ignores an Escape something else claimed (a picker of the composer, which stays usable while the card waits). A tip, menu or popover that listens on **`document`** or **`window`** for Escape does it in the **capture** phase and calls **`preventDefault`** - which is what **`useEscapeCloses`** does, and what the History row and filter menus and **`ConfirmDialog`** do on **`window`**: in the bubble phase on **`document`** it would come after the screen, which has listened since before it opened, and both would close.
- **A hidden pane is deaf.** While the results of the settings search show on the wide drawer, the tab under them is hidden, not unmounted, and **`PaneHidden`** (**`components/PanePortal.tsx`**, a context the drawer sets to **`true`** around that tab) is how everything inside it knows. **`useEscapeCloses`** does not listen while it is **`true`**, so a tip, a tag editor or the Skills install results in the hidden tab claims no Escape and the key goes to the results step above instead; **`PanePortal`** draws nothing into **`<body>`** while it is **`true`**, so a portal of the hidden tab does not sit over the results. Build an Escape layer under Settings on **`useEscapeCloses`** and draw its portal through **`PanePortal`**: those two are the chokepoints, and a layer that listens or portals on its own is not covered (**Settings drawer**, *The hidden tab*).
- **What never closes a screen**: a held key (**`repeat`**: one press, one step), Escape with a modifier, and the Escape that ends an input method's composition (**`isComposing`**, or keyCode **229**).

Tests: **`nav/railEscape.test.tsx`** (the stacking order, the claim, a held, modified or composing key, the steps and whose they are, the chat under a screen), **`App.railEscape.test.tsx`** (every screen of the table through the real app, the scheduler's and Settings' steps, the search, a row menu of History, the backdrop and the relay's home), **`components/useEscapeCloses.test.tsx`** (the claim, a screen under the layer, a layer in a hidden pane, **`PanePortal`**), **`Settings.test.tsx`** (**`describe("settings search")`**: Escape's steps, Telegram's Back, each layer of a hidden tab), and in their own tests the claims of **`Combobox`**, **`FieldHint`**, **`NavRail`**, **`ContextBreakdownPopover`**, **`ChatHeader`**, **`QuestionPromptSection`**, **`SkillsSection`**, the composer's menus (**`ComposerModelMenu.test.tsx`**), **`EnvironmentSwitcher`**, **`WorkspaceChips`** and the folder browser, each against a screen that listens from before it opened where a screen can be under it (**`nav/railEscape.fakes.tsx`**). Scenarios: **`features/web_ui_rail_escape.feature`**.

Sessions search uses **`GET /coddy/sessions?q=...`** (**title or first persisted user message substring only**); list uses infinite scroll toward older pages.

Desktop navigation wider than Full HD optionally shows labels on the expanded rail (**cookie** remembers preference).

Sessions list interactions

- Session list supports open on row click.
- Session list shows a small trash icon on hover.
- Renaming is done only in the chat header.
