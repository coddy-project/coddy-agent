# UI design: components

[Design index](../../DESIGN.md)

### Markdown

**Assistant** messages may contain Markdown. **User** bubbles do not (plain **`pre-wrap`** text).

- **Nothing in an answer is wider than the transcript.** **`.md`** breaks a long link, path or word where it has to (**`overflow-wrap: anywhere`**). Inline code (**`.md-inline-code`**) is an **18px** chip - a **14px** line box and **3px** + **1px** of padding, the text's middle **10px** under the top edge - at most a line wide, and a long identifier wraps inside the chip, **14px** a line; the chip used to carry a **10px** line box, which overlaps the next line as soon as the text wraps. Tables (**`.md-table-scroll`**) and fenced code keep their width and scroll in their own box (**`transcriptWrapCss.test.ts`**).

- Inline code is ordinary, selectable text inside the sentence. It is not a focusable copy control, so screen readers read the command in context without an extra button announcement; copying uses the browser's normal text selection.
- A single newline inside a paragraph is CommonMark's soft break, a space, in an answer, the documentation and every other body. Only the canvas cards ask **`Markdown`** for **`breaks`**, Obsidian's reading, where it is a line break (*Canvas*).
- **`coddy-skill:`** chips appear only in the **composer mirror** while editing, not in persisted user bubbles.
- **A path an answer names is a link to the Files window** once the server says it is a file of the chat's workspace (**`POST /coddy/sessions/{id}/workspace/stat`**, asked through **`files/workspaceEntries.ts`**: the paths of a moment in one request, an answer kept for the page, "not there" for **30 s**). Three spellings count, read by **`markdown/fileReferences.ts`**: inline code (**`parseFileReference`**: a name alone counts, **`README.md`**, **`Makefile`**, **`.gitignore`**), a link target (**`localLinkReference`**: relative, absolute or **`file://`**) and prose (**`splitProsePaths`** through **`remarkFilePaths`**, which writes a **`coddy-file:`** link: a folder and an extension are needed, since prose is full of **and/or**). A **`:12`**, **`:12:7`**, **`:12-20`** or **`#L12`** suffix names the line. The link (**`FileLink`** in **`markdown/FileReference.tsx`**, **`a.md-file-link`**) takes the doc mention's look - accent-tinted text, a faint underline that firms up on hover, **`overflow-wrap: anywhere`**; inline code keeps its chip in the link's colour, underlined only on hover. Its **`href`** is the window's own address (**`sessionFilesHash`**), so a middle click opens the file in a new tab; a plain click goes through **`openWorkspaceFile`**. Anything the server does not confirm as a file (a folder, a link, a path outside the workspace, a name it does not have, a failed request) keeps its original form, as does every path of an answer still streaming. Inline code inside a link is that link's label (**`InsideLinkContext`**, set by every link the Markdown draws): plain code with no separate file link of its own and no tab stop, so a link whose text is its own path in code opens the file once, and a link to the web is followed. Only the transcript provides the chat (**`WorkspaceFilesContext`**, set by **`ChatScreen`**), so the documentation, a canvas card and every other body ask nothing. Covered by **`fileReferences.test.ts`**, **`FileReference.test.tsx`**, **`workspaceEntries.test.ts`** and **`features/web_ui_file_links.feature`**.
- **A picture or a video from another site waits for a click** (**`RemoteMediaGate`** in **`markdown/Markdown.tsx`**, **`.md-remote-media`**): a button in the picture's place, a dashed hairline at **55%** of **`--muted`**, **6px** radius, the alt text on its first line in **`--text`** and **Load the picture from <host>** under it at **0.85em** in **`--muted`**; hover and focus turn the border to **`--accent`**. A click puts the picture in its place and, for a picture inside a link, leaves the link alone, and a consent belongs to one address (**`key`**). A Mermaid block whose text holds shape data (**`@{`**) can fetch as it is drawn, so it waits the same way with its source showing (**`mermaidMayFetch`**, **`md-figure-draw`**), and a directive or a front matter cannot set a font, a theme, a stylesheet or HTML labels (**`secure`** in **`pictureRender.ts`**). Same-origin, relative and **`data:`** addresses load at once, and the documentation reader passes **`remoteMedia="load"`** for its own screenshots and videos. Covered by **`Markdown.test.tsx`**.
- Render fenced code blocks with syntax highlighting.
- The explicit language registry and aliases live in `external/ui/src/ui/markdown/syntaxLanguages.ts`. It extends the common grammars with the languages covered in [the NeuralDeep audit](../../docs/contributing/syntax-highlighting-audit.md); six additional grammars retain pinned upstream sources and licenses under `ui/markdown/grammars/`. PL/SQL, OpenCL, and CUDA use base SQL/C/C++ highlighting. Unsupported labels remain literal text; never guess a related language silently.
- Treat `vue` as an HTML/XML grammar alias for component tags, attributes, and comments, with standard embedded JavaScript and CSS highlighting. This is not a Vue compiler: interpolation expressions and alternative `lang` preprocessors do not receive dedicated grammars.
- Treat the `postcss` fence label as a CSS grammar alias; retain the original label and source for rendering and copying. Plugin-specific PostCSS extensions have CSS-level highlighting only.
- Use the declared fence language (`js` / `javascript`, `ts`, `css`, `html`, `json`, `python`, `go`, and other bundled highlight.js common languages). Unknown or unlabelled fences remain literal text without language guessing; incomplete streamed fences still render safely.
- Syntax colors use the `--syntax-*` semantic palette in each of the eight appearance themes. Keywords, strings, numbers, titles, attributes/selectors, types, comments, metadata, and deletions follow the active theme immediately, including already-rendered responses. Keep token selectors scoped to the views that colour code, never bare: `.md-code` (the chat's code blocks), `.files-code` (an open file of the Files window) and `.dv-code` (a diff of the edits window), one `:is()` list each, held by `files/filePreviewCss.test.ts` (the two windows carried the classes without the colours until it was).
- Each code block has a copy button in the top right corner that copies only the block contents. The block keeps its own line box (**12px** at **1.5**, whatever the text around it) and the button sits on the middle of its first line (**`top: 3px`** for a **26px** button over **1px** border, **6px** padding and an **18px** line), so a one-line block has it centred (**`codeCopyButtonCss.test.ts`**).

### Diagrams and formulas

A **`mermaid`** or **`svg`** fence and a display formula (**`$$...$$`**, **`\[...\]`**, a **`math`** fence) are a **figure** (**`.md-figure`**), not a code block; an inline formula (**`$...$`**, **`\(...\)`**) is typeset in the line. Behaviour: **`docs/surfaces/web-ui.md`** (**Diagrams and formulas**).

- **Frame.** The code block's frame: **14px** radius, the same hairline border, **`--coddy-surface-inset`** behind it, **12px** above and below. It never grows wider than the transcript.
- **Head** (**`.md-figure-head`**): the kind on the left (**12px**, muted), the actions on the right in one row, all in the **`md-copy`** chrome and set in the row rather than floating over the content: the formats the figure saves as, named by extension (**`.md-figure-format`**: **MMD** for the Mermaid source and **SVG** for the drawn picture; an SVG fence has **SVG** only, its source), then the source switch (**`.md-figure-source-toggle`**), an icon of angle brackets with **`aria-pressed`**: off while the picture is shown (the default), lit with a **16%** accent tint and a **45%** accent border while the source is. A formula has the source switch only. The head wraps on a narrow column; the actions keep together at its right edge.
- **Picture** (**`.md-figure-picture`**): centred with **12px** around it, at most the column wide and **min(70vh, 640px)** tall, proportions kept, **`cursor: zoom-in`**; it is a button that opens the image viewer (**`ImageLightbox`**). The picture is an `<img>` of a `data:` SVG, never SVG in the DOM.
- **Source view and errors** (**`.md-figure-source`**): the source as a code block body (**12px** at **1.5**), scrolling in its own box once it is longer than twenty lines (**`max-height`** of **20 × 18px** plus its padding), with the copy button in the box's top right corner, clear of the scrollbar, as on a code block; copy lives only here. A failed draw puts one **12px** line in **`--coddy-danger`** between the head and the source, and the source switch is pressed and disabled.
- **Formulas.** A display formula scrolls sideways inside **`.md-math-display`**. An inline formula (**`.md-math-inline`**) is KaTeX at **1.1em**, **`cursor: copy`**, an accent tint on hover and for a moment after it was copied, and its source as the tooltip; until KaTeX has loaded it shows its source in the inline-code font.
- **Colours.** A Mermaid diagram is drawn with Mermaid's **`base`** theme and every colour a diagram type draws with is set from the active appearance (**`mermaidThemeVariables`** in **`ui/markdown/pictureRender.ts`**), never left for Mermaid to derive (that gave near-black pie slices and invisible edges on dark themes). Tokens are resolved by the browser and composited onto **`--coddy-surface-inset`**, so a **`color-mix()`** or an alpha token works. Fills are the accent or a categorical colour mixed into the surface (**22%** for nodes, **35-45%** for Gantt bars and mindmap sections) under **`--text`**; lines and edges are **`--text`** at **70%**; marks that carry a label (pie slices, git branches, chart series) take the categorical colours at full strength - the theme's **`--accent`** and **`--syntax-*`** palette, deduplicated and each pushed away from the surface until it contrasts with it by **4.5:1** - and are labelled in the surface colour. **`darkMode`** for every theme but **light**; drawn again on a theme switch. Contract: the contrast tests in **`DiagramsAndMath.test.tsx`**; live check **`npm run check:diagrams`**.

### Memory tree (deferred explorer)

A file-tree over combined **global** (**`memory.dir`** / `$CODDY_HOME/memory`) and **workspace** (`<cwd>/.coddy/memory`) remains out of scope for this milestone.

### Memory runs

When **`memory.enable`** is true, the bundled **`memory`** subagent definition is offered to **`spawn_agent`**, and a delegation to it runs as an ordinary **`agent`** task. Its record is the **Tasks panel**: an agent task whose card carries the **`memory`** tag (**`bgtask-tag`**) like every agent run, the first line of the message as its title, **Show transcript** to the read-only child session on the card itself, and the child's log in the open card.

### Component boundaries

The UI should be implemented as small React components with folder-enforced hierarchy.

- `ui/layout/Shell`
- `ui/nav/NavRail`
- `ui/sessions/SessionsSidebar`
- `ui/chat/ChatScreen`
- `ui/messages/MessageList`
  - `ui/messages/UserMessage`
  - `ui/messages/AssistantMessage`
  - `ui/messages/ThinkingMessage`
  - `ui/messages/ToolCallMessage`
- `ui/chat/PlanDocumentSection`
- `ui/markdown/MarkdownLineEditor`

### Session overflow menu (`…`)

Opens lightweight rename/delete UX (prompt-first until richer modals arrive).

### Swarm screen (`ui/swarm/SwarmView.tsx`)

Shown at **`#/swarm`** in any environment that answers **`GET /swarm/info`**, and as the **home
screen** when the environment is a relay itself. It is the only screen a relay has, and the map
is the screen: there is no list of nodes under it, because everything the list did the map does.

- **Relay as home.** A relay serves no sessions, no workspace and no model - of an agent's API it
  serves only its own settings (**`/coddy/config*`**, see **The relay's own Settings** under **Settings drawer**). **`App.tsx`**
  tracks this as **`atSwarmRoot`** (the swarm probe answered and the environment is not a node
  reached *through* a relay). While it is true the composer and **`ChatScreen`** are not rendered,
  and the rail hides **History** and **Scheduler** - a drawer of sessions that cannot exist is
  furniture for a room nobody can enter. The map is the home screen: the **Swarm** entry of the
  rail stays lit while the map is on screen, whichever address shows it, and the brand, which
  starts a new chat on an agent, leads to the map (a relay has no chat to start). A relay that
  serves its own page has no documentation either, so there the rail leaves **Docs** out (the
  reader reads the page's own server, see **Documentation screen**).
- **The map is the way in.** Clicking a node in the graph switches the app to it: the app repoints
  at that node through the relay (**`connectSwarmNode`**, landing on **`#/swarm`**) and the map
  stays open over it, the node now ringed; every ordinary screen - chat, History, Scheduler,
  Settings - then works against it, and what to do there is the person's next click. The click
  does nothing more: not even a node that asks a question opens that session by itself. A relay on
  the map opens as a relay (**`connectSwarmRelay`**), never as a node whose screens it does not
  have: one chained under this one opens on its own map through this one's mount, and the relay
  the map is drawn for is enterable only from a map opened over a node, where a click on it
  connects to the relay. Where the app already is - the node it is on, the relay on its own map -
  is not a button: a click there would go nowhere. A node with no route is not clickable either.
  Enter and Space do what a click does, and an enterable node takes a visible focus ring.
- **A switch does not blink.** Between two remotes (a node and another node, a node and its relay)
  the page is not reloaded: **`switchTo`** (**`env/remoteEnv.ts`**) sets the environment and the
  address and runs what the modules that hold something of the old one registered with
  **`onEnvironmentSwitch`** (the Settings copy, the server's version, the host shell, the active
  environment's health), and **`EnvScope`** (**`main.tsx`**) starts **`App`** over, keyed by the
  events stream's scope (**`serverEventsScope`**: the server and a fingerprint of its token) and
  the count of switches in place, so the same remote chosen again and a token rotated under the
  one in use start it over as well. What would otherwise start as "not known yet" and pop in a moment later is
  kept for the life of the page (**`env/pageMemory.ts`**): the page's own server and its host name,
  which remotes are relays, whether the scheduler answered, and the last picture of each relay's
  map, which a remounted map draws at once and then reads again. To or from **Local** the page
  still reloads, since the sign-in of the page's own origin comes and goes with it.
- **The map over a node.** Inside a node the **Swarm** entry opens the relay's map over the node
  without leaving it: the environment stays the node's mount, the map reads the relay directly
  (**`SwarmView`** **`relay`**, **`swarm/api.ts`** **`RelayTarget`**: the relay's address and the
  client token the mount takes), and History, Scheduler and the chat stay where they are, so
  nothing reloads and nothing blinks. History and Scheduler lie under the map in the stack of
  the rail's screens, so opening either from the rail closes the map rather than opening out of
  sight under it.
- **Where we are, and how we got there.** Inside a node the map marks it (**`env.swarmNode`**): it
  carries a ring, and every edge on its route is drawn as the live path, with everything
  off the route receding. The ring is the mark: no line of words says it again. On the relay
  itself (**`rootCurrent`**, the app on the relay's home) the relay's card carries the ring, and
  the wire from this machine, when the map draws it, is the live path. Hovering or focusing another node previews its route the same way,
  weaker.
- **The machine the page runs on.** When the page's own server is an agent (not a relay serving its
  own page) and the app is on a remote environment, the map draws that machine one tier above the
  relay, wired to it - it is where the connection starts: a disc with a screen glyph, named by its
  host name (**`GET /coddy/info`** of the page's server; **Local** when the system reports none),
  on a tier the spine calls *this machine*. The live route to the node the app is on starts there. A click on it
  goes back to Local (**`connectLocal`**). Layout: **`layoutTopology(topology, {client})`**
  (**`swarm/layout.ts`**) lifts every tier of the swarm by one and adds the client and its wire,
  which leaves the disc below its name and address, not through them.
- **What the swarm is doing.** **`GET /swarm/sessions`** carries **`turnActive`** and
  **`permissionPending`** per session; **`nodeActivity`** (**`swarm/routes.ts`**, pure and tested)
  folds them per node path. Under each node's meta line: nothing when it holds no sessions,
  a session count when it is idle, a running count when a turn is in flight, and *needs an answer*
  when anything there waits on a permission prompt - that state wins, because it is the one that
  needs a human. A running node pulses slowly, a waiting node pulses sharply in a different
  rhythm, and the hops on the route to a running node carry a travelling dash. Every one of those
  is driven by that live data and by nothing else, and
  **`@media (prefers-reduced-motion: reduce)`** removes all of it, leaving the states carried by
  the copy and a static ring.
- **Dock.** The map opens in a dock as wide as the documentation reader's and centred the same way:
  both take **`width: var(--coddy-dock-width)`**, defined once on **`.shell`** as
  **`min(1350px, …)`**, and the map fills its dock (**`.swarm-view`** has no width cap of its own),
  so a wide screen shows no bare strips beside it. Pinned by **`SwarmView.test.tsx`** (**opens in a
  dock as wide as the documentation reader's**) and **`docsReaderCss.test.ts`**.
- **Dock on a phone.** Below **1200px** the shell's backdrop rises to **`z-index: 60`** and **`#/swarm`** opens it, so **`.swarm-dock-cluster`** takes **70** there, like the settings drawer and the documentation reader, and starts under the top bar the rail becomes (**`top: calc(var(--coddy-mobile-top-inset) + 6px)`**, the side insets clearing the safe area). Under the backdrop every tap on the map, the search box or a node landed on the backdrop and closed the screen. Pinned by **`SwarmView.test.tsx`** (**takes taps on a phone**) and **`features/swarm_web_ui.feature`**.
- **Header.** Title (relay name) and a subtitle counting relays, agents and offline nodes, then
  **`.swarm-header-actions`** with the close control. The environment is not here: it is the
  rail's item (**Environment item and menu**), on every Swarm screen too, its empty and error
  states included, so a relay that needs a token can still be given one. Pinned by
  **`nav/EnvironmentSwitcher.test.tsx`** and **`features/web_ui_menus.feature`**.
- **Canvas layout and persistence.** The graph opens in **Tree layout**: the rooted, tiered view
  with its hop spine. **Graph layout** is a deterministic rooted graph for reading rings and
  cross-links. It trends down by shortest-route depth without placing every hop on a rigid row, and
  draws its links as smooth curves. Its fixed, UUID-ordered solver makes the same topology land in
  the same places on every poll rather than jittering as the relay enumerates it. The relay is the
  pinned root, or the local computer is the root when that connection-start node is present. The
  choice is browser state only, stored in localStorage as **`coddy_swarm_layout`**; a saved legacy
  `star` value migrates to Graph, and an absent, blocked or invalid value means Tree. It never
  changes server configuration or another browser's choice.
- **Canvas camera.** The graph is fitted into its viewport on first open and whenever the relay or
  layout changes. The **Tree** and **Graph** controls select the layout; the shared zoom controls (**Zoom**) -
  **Zoom out**, the level (the fit glyph while untouched, the percentage once the operator zooms or
  pans; a click fits the graph) and **Zoom in** - control the camera. Pointer-wheel zoom is centred on the pointer, dragging pans
  across both axes even in the fitted view, and a two-finger pinch zooms around the pinch midpoint;
  a drag or pinch never also enters a node. When the canvas has focus, **`+`** / **`=`** zoom in,
  **`-`** zooms out and **`0`** fits. Zoom never goes below the fitted view or above three times
  that scale, and panning remains clamped so the graph cannot vanish completely. The Tree/Graph buttons match the zoom controls' **28px** height at every viewport width;
  their width stays **40px** on the stacked shell. A polling update fits an untouched graph to its new
  bounds, but preserves a manually panned or zoomed camera (clamping it only if the bounds moved);
  Fit explicitly resets that manual view.
- **Search, not filter.** The box goes to the relay, which fans out, so a query reaches machines
  this browser cannot dial. With a query, matching sessions appear as rows under the map, each
  naming its node and route, and a row opens that session on that node. With no query there are
  no rows at all.
- **Distinct empty and error states.** **`/swarm/info`** is public, so a credentialed relay answers
  the probe and refuses everything else; the view then shows **`.swarm-error`** asking for a token
  rather than reporting an empty swarm. Nodes that did not answer are listed in
  **`.swarm-warnings`** above the map rather than silently dropped.
- **The topology graph** is a hand-rolled SVG (**`TopologyGraph.tsx`**, layout in
  **`swarm/layout.ts`**). A relay is a card carrying an accent-filled tile with the router mark; an
  agent is a circle with its name on a chip below. Hops are orthogonal elbows that leave the
  bottom, turn on a rail shared by the children of one parent, and arrive at the top, with no
  arrowheads: the tiers say which way a hop runs. A same-tier link between peers is a bow that
  leaves and arrives at the sides; a link that skips a row goes round the outside in a lane clear
  of every card. A wire is named (a chip on it) only when the relay reaches the node under a mount
  name other than the node's own, which the chip under the node already shows. Depth is stated in
  the left gutter - a dashed upright with a tick, a hop caption and a node count per tier. Every
  state is said twice, never in colour alone: the route in use is solid, heavier and in the
  accent, a way round a ring is grey and dashed, a link into an offline node is coarsely dashed and
  dimmed, and a node that dials out carries a badge as well as a dotted wire. Every relay on the
  active route is outlined, while the current relay has the stronger current-node ring. The SVG is a
  **`role="group"`** labelled by the map's name - not **`role="img"`**, which would collapse the
  subtree and hide the interactive per-node controls it carries - and a visually hidden paragraph
  added through **`aria-describedby`** names each tier, its nodes, where the app is and what is
  running. The SVG keeps its intrinsic
  size and scrolls inside **`.swarm-graph-viewport`** rather than scaling its labels below
  legibility on a phone, and the legend below it wraps instead of setting a minimum width.
- **Words on the map are for what the drawing cannot say.** Under a node there is a line of words
  only for trouble - *offline*, *no route* - and on a relay's card, how many links it carries
  (**N links**, the wire from this machine not counted). The rest is drawn: the card or the disc
  says relay or agent, the ring where the app is, the badge and the dotted wire a node that dials
  out, the screen glyph and the tier this machine. With no line of words under a node its work
  (a session count, *running*, *needs an answer*) takes that line. A hover still spells it all
  out in the node's tooltip (**`<title>`**: name, what it is, the line, the work). Pinned by
  **`SwarmView.test.tsx`**.

The relay serves this SPA from its own address when built with **`-tags "swarm ui"`**
(**`external/swarm/spa_ui.go`**), so a relay is something you open in a browser rather than a
service you reach through some other node's UI.

### Documentation screen (`ui/docs/DocsView.tsx`)

Coddy's own documentation, read out of the binary (**`GET /coddy/docs*`**), at **`#/docs/<page>#<section>`**. It is a reading surface, so it takes the swarm screen's glass dock (**`.docs-dock-cluster`**, the same fixed inset, radius, border, tint and blur as **`.swarm-dock-cluster`**) and reuses the app's controls rather than drawing its own. It is a **sheet**, not a floor: it grows no wider than its three columns (**1350px**: 240 + 780 + 210, two 32px gaps, 28px either side) and stays centred in the space right of the rail (**`width: var(--coddy-dock-width)`**, **`min(1350px, ...)`** defined once on **`.shell`** and shared with the swarm map's dock, with **`margin-inline: auto`**), so a wide window neither frames the page in empty glass nor pushes the outline away from the text (**`docsReaderCss.test.ts`**).

**The pages come from the page's own server**, whatever environment the app is on: **`docs/api.ts`** reads them with **`localFetch`**, past the **`fetch`** shim, because the documentation a person reads is the one of the Coddy they opened, and a relay serves none (it answers only its own routes). A node reached through a relay still serves its pages, for the agent's **`@coddy:`** mentions there. On a relay's own page (the relay serving the SPA from its root) the rail has no **Docs** entry, since there is nothing to read. The sheet does not scroll: the header stays put and the page scrolls in the body under it (**`.docs-body`**, **`overflow: auto`**, a size container), so the sheet's scrollbar starts below the search box instead of running up beside it. Reference capture: **`docs/assets/built-in-docs/reader-page-dark-1280.png`**.

- **Header** (**`.docs-header`**) stands over the columns of the page (the same tracks, the outer two wider by what its smaller inset leaves them, and the **32px** gap of the layout, areas **title search actions**): the title over the contents, the search over the text and as wide as it (at most **780px**), the actions over the outline. The actions take the outline's own column, not one as wide as themselves: with an **`auto`** column (160px in English, 182px in Russian, against the outline's 210px) the search column grew by the difference and the box ran on into **On this page**. The header's insets are the same on both sides (**`--docs-inline`**: **28px**, **16px** on the stacked shell), as in every other panel head: the close button stands as far from the right edge as the title from the left. Only the body scrolls, so a classic scrollbar (10px on Linux, none with overlay scrollbars) takes its width out of the body alone; **`DocsView`** measures it (**`offsetWidth - clientWidth`** of **`.docs-body`**, on a **`ResizeObserver`** of the body and the layout, since the scrollbar comes and goes with the page's height) into **`--docs-scrollbar`**, and the body gives that much out of its right padding (**`max(0px, calc(var(--docs-inline) - var(--docs-scrollbar, 0px)))`**), so the scrollbar stands in the margin, the text ends as far from the right edge as it starts from the left and the search ends exactly where the text does at every width. The header adding the scrollbar to its own right padding instead kept the columns but put the close button and the search 10px farther from the right edge than the title from the left (**`docsReaderCss.test.ts`**, **`DocsView.test.tsx`**). The body keeps the scrollbar's room from the first frame (**`scrollbar-gutter: stable`**), so nothing moves when a page grows tall enough to scroll after it loads (**`npm run check:files`** reads the close button's position every frame with the scrollbars shown, and its inset against the title's). Between **1200px** and **1279px**, where the outline is gone, the actions go back to an **`auto`** column. The title **Documentation** is spelled like the head of every other panel (**`.sessions-head`** of History, Scheduler and Tasks: **12px**, **700**, uppercase, **0.06em** tracking, a **72%** text tint), with the version alone in **11px** at **52%** under it (where the pages come from is not news on this screen). The actions are **Ask the agent** (**`.docs-ask`**, a **999px** pill **30px** tall - the height of the close control beside it, as every row of controls keeps one height - on a **16%** accent plate with a **45%** accent border, the accent's own action because it starts a conversation; the longer explanation is its **`title`**) and the shared close control **`.sessions-close`** - never a bare glyph. The header sits on an **82%** canvas plate with a **6%** hairline under it, outside what scrolls. On the stacked shell it becomes two rows: the title with the actions, then the search under them, across the whole sheet, as the page below it is (**`docsReaderCss.test.ts`**).
- **Three columns** from **1280px**: the contents (**240px**), the page (a reading measure of at most **780px**), **On this page** (**210px**); below **1280px** the outline goes, below **1200px** (the stacked shell) the contents fold into a **Contents** button with **`<Chevron pointing="down">`** above the page, and the page has the sheet to itself: the **780px** reading measure is the desktop's, and on a tablet it only left an empty strip beside the text and the search, so there the search, the **Contents** button and the page stretch across the sheet (**`.docs-article`** and **`.docs-header-search`** at **`max-width: none`**). **On this page** never leaves: where it has no room beside the page it folds into a second button under **Contents** (**`.docs-outline-toggle`**, the same frame, **`<Chevron pointing="down">`**, the list under it on **`.is-open`**, closed again by a section link and by a page change), the column's areas being **toc** / **outline** / **page**. **From 900px** on the stacked shell (a row of the grid's component table) it stands beside the page instead, sticky as on the desktop: **`minmax(0, 1fr) minmax(190px, 210px)`** with the areas **toc outline** / **page outline**, the page taking the rest of the width and the outline at least **190px**, which the header's actions need in Russian; the contents stay folded above the page. Below **900px** the page would drop under about 600px beside it. Both side columns are sticky at the top of the body (**18px**, as tall as the body less that gap at either end: **`calc(100cqh - 2 * var(--docs-top))`**), scroll on their own with the thin scrollbars of Settings, and pad themselves by **3px** so a focus ring is never clipped.
- **Contents** (**`.docs-toc`**): group titles in **11px** uppercase at **52%** text, set **10px** in; the pages of a group sit **12px** further right than that (**`.docs-toc-group > ul`**, **`padding-left: 12px`**), so the title reads as the heading of its list and not as one more page of it - flush, the two ran together. Each page is a **`.docs-toc-page`** drawn exactly like a Settings tab (**`.settings-nav-item`**: **10px** radius, the **6%** text-tint hover, the page being read on a **22%** accent plate with a **40%** accent border and **`aria-current="page"`**).
- **Search** (**`.docs-search`**) is the swarm screen's field (**`--coddy-surface-field`**, **10px** radius, **14px** text, a **2px** accent focus outline), with a **`/`** key cap at its right end while it is empty. The hits hang under it (**`.docs-search-results`**, **12px** radius, the panel border and shadow, at most **62vh**) while the box is in use, and the contents stay where they are. The panel is **opaque** - the canvas colour with a **5%** text tint layered on it - because it lives inside the blurred header, where a nested blur sees nothing and a glass tint lets the page show through. The list (**`.docs-hits`**, a **`role="listbox"`** the input controls as a **combobox**) reads page **›** section, then a three-line snippet whose matched words are **`<mark>`**s on a **24%** accent plate; the selected hit (**`aria-activedescendant`**, also the one under the pointer) sits on a **14%** accent plate with a **32%** accent border. Opening a hit folds the panel away; focusing the box brings it back; Escape clears it, and in an empty box goes on to close the reader (**Escape and the screens of the rail**).
- **The page** (**`.docs-article .md`**) reads, it does not chat: **15px** at **1.68** line height, **h1 28px**, **h2 20px** with a hairline under it and **36px** above, **h3 16px**; links in the accent mixed **72%** with the text, underlined at **45%**; images fit the column with the panel border. Every section heading carries a **`#`** link (**`.docs-heading-anchor`**) that shows on hover or focus. A breadcrumb (group **›** page) and a quiet **coddy.dev ↗** link sit above the title. While the next page loads, the one on screen stays at **55%** opacity (**`.is-loading`**, **`aria-busy`**) and the ask pill is disabled.
- **Images and videos.** An image of the page shows **`cursor: zoom-in`** and opens in a lightbox (**`.docs-lightbox`**, rendered into the body at **`z-index: 400`** over an **88%** canvas veil with an **8px** blur): a bar with the caption, the shared zoom controls (**Zoom**: **−**, the level, **+**) and the shared close control, over a stage that scrolls a zoomed image. The level puts the image back to fit: the fit glyph while the image is fitted, the percentage while it is zoomed (the words are its **`title`**). A zoom keeps the point clicked, the point between two pinching fingers, or the middle of the view for the keys and the buttons, where it was. The stage takes the gestures itself (**`touch-action: none`**, which lives with the stage and so leaves the page behind it alone): one pointer drags the picture within what there is to scroll, two fingers pinch it, and the wheel zooms around the pointer instead of scrolling - a listener of the stage's own, **not passive**, so the page behind the viewer does not scroll either (**`WHEEL_DOUBLING_PX`** 500, and lines and pages are read as pixels first). A video (**`video.md-video`**) fills the column with a **10px** radius and the panel border, on black until its first frame (**`#t=0.1`**) arrives.
- **`/docs`** in the composer opens this screen rather than sending a prompt, and the slash menu's **Commands** group lists it (a row the browser adds to the server's catalog, only where the composer can open the reader). With words it opens the reader with them typed in the search box and the hits open under it.
- **The foot** (**`.docs-pager`**): the page before and the page after as two cards on **`--coddy-surface-subtle`** with the panel border, the direction in **12px** muted text over the title; the accent frames the one under the pointer.
- **On this page** (**`.docs-outline`**): the sections two levels deep on a hairline rule, the one being read (it follows the scroll) marked by a **2px** accent segment of that rule and the full text colour.
- Every transition here is removed under **`prefers-reduced-motion: reduce`**. The screen follows every theme through the tokens only (**`--text`**, **`--bg`**, **`--accent`**, **`--coddy-blend-base`**, **`--coddy-glass-panel-*`**, **`--coddy-surface-*`**): no colour of its own.

### Sign-in screen (`httpserver.login`)

Rendered by **`AuthGate`** (**`ui/auth/AuthGate.tsx`**, wrapping **`<App/>`** in **`main.tsx`**) in place of
the entire application when **`GET /coddy/auth/me`** answers **`login_required`** without
**`authenticated`**. Only on the local origin: a remote environment authenticates with the bearer
token of the environment selector and is never gated here.

- **The whole page, not an overlay.** **`.auth-screen`** is a full-viewport grid centred on
  **`.auth-card`** over the plain **`--bg`** ground. There is no blurred transcript behind it,
  because an anonymous browser has nothing to be shown.
- **Boot.** While the one **`/coddy/auth/me`** round trip is outstanding the gate renders
  **`.auth-boot`** — a bare **`--bg`** ground, no spinner and no application chrome. Rendering the
  app and then replacing it with a form reads as a glitch; a spinner for 20 ms reads as a slow page.
- **The column** (**`.auth-shell`**, **`min(380px, 100%)`**) holds two things with a **22px** gap:
  the wordmark, then the card. The logo stands **above** the card and outside it, because it names
  the product while the card asks for one thing.
- **The card** (**`.auth-card`**, the column's full width) uses the glass panel tokens
  (**`--coddy-glass-panel-bg`** / **`-border`** / **`-backdrop`** / **`-shadow`** / **`-radius`**),
  **28px** padding and a **14px** column gap: **`h1`** title, the fields of the step, the error
  line, the submit button. The first step asks for the user and the password and nothing else.
  When the account asks for a one-time code, a correct password turns the card into its second
  step instead of signing in (**`.auth-step`** *Step 2 of 2* over a **Two-step verification**
  title): one code field (**`.auth-field`**, **`autocomplete="one-time-code"`**,
  **`inputmode="numeric"`**), a quiet bar counting down the current authenticator period,
  **Verify and sign in**, and **Use a backup code** / **Use an authenticator code** switching the
  field between the 6-digit code and a backup code (which takes text). **Back to login** drops
  the challenge. A refusal clears the password and the code, because a code - or the challenge a
  completed verify call took - is spent once. Nothing explains the screen in prose - a form with
  two fields under a title that says Sign in needs no caption.
- **External sign-in (OIDC)** replaces every local route: when **`/coddy/auth/me`** answers
  **`oidc_enabled`**, the refresh runs none of the alternate sign-ins (the Mini App launch data and
  the panel ticket are never posted, even when **`telegram_login`** or **`browser_login`** is also
  advertised) and the card holds a single link (**`a.auth-oidc-link`**, drawn like the submit
  button) that hands the browser to the issuer with a same-origin GET of
  **`/coddy/auth/oidc/start`** - a top-level navigation, never a fetch. No password field, no
  one-time or backup code field, no Telegram note and no QR stands beside it: the cookie the
  provider's callback sets is the only way in. A trusted desktop is unchanged, because the
  desktop shell's trust lives server-side and never reaches this screen.
- **The wordmark** (**`.auth-logo`**, **`min(240px, 72%)`** of the column, centred) is the
  rectangular logo with the product name in it, not a text line:
  **`src/assets/coddy-logo-wordmark.svg`** on the six dark themes and **`-light.svg`** on
  **`light`** (**`LIGHT_THEMES`**). **`aspect-ratio: 188 / 56`** holds its height, so nothing below
  it moves while the image decodes. Both files are under Vite's inline limit, so they travel inside
  the bundle and the one screen a browser sees before it has any credential paints without a second
  request. Its **`alt`** is a dictionary key (**`auth.signIn.logoAlt`**), because the name in the
  image is what a screen reader must hear.
- **Fields** are ordinary **`label`**-wrapped inputs (**`.auth-field`** / **`.auth-label`** /
  **`.auth-input`**) with **`autocomplete="username"`** and **`"current-password"`** so a password
  manager fills them. Focus draws the accent ring (**`--accent`** at 55% border, 25% glow), never a
  browser default outline. The user field takes focus on mount.
- **The error line** (**`.auth-error`**, **`role="alert"`**) replaces nothing else: the card grows by
  one line. A refused sign-in clears the password field and leaves the user name alone.
- **Submit** (**`.auth-submit`**) is accent-filled, disabled while either field is empty and while a
  request is in flight, and swaps its label for the working one meanwhile.
- **Colour comes from tokens only**, so all eight themes carry the screen, and every string is a
  dictionary key (**`auth.signIn.*`**), so both languages do.
- **Signing out** is the last entry of the nav rail (**`data-testid="nav-sign-out"`**), below
  Settings, rendered only while a form is configured and this browser has passed it. Narrow rail:
  icon plus a tooltip naming the account (**`Sign out (pasha)`**); wide rail: icon plus label.
  A **`POST /coddy/auth/logout`** that succeeds reloads the page, unless the answer marks a
  continuation: the **`X-Coddy-Logout-Continue: 1`** header has the rail navigate the top level to
  same-origin **`GET /coddy/auth/logout/continue`** instead, where the server spends the ticket it
  left and sends the browser to the configured **logout_url**, or back here when there is none. A
  refused or unreachable POST does nothing - no reload and no navigation - so a session the server
  still holds is not pretended away.

### Desktop shell (`external/desktop/shell`)

The page the desktop window opens on, served at **`/desktop/`**. It is not part of the SPA: one HTML
file, one stylesheet, one script, no React and no vite entry, because it is a frame **around** the
SPA and a window backend has to reach it whether or not the SPA build has run. It draws a tab strip
over one **`<iframe>`** per chat, each frame the web UI of this same server.

- **The strip** is a 38px header with 5px padding above and 8px at the sides, and with no system frame it is the
  window's title bar as well. On every platform the strip starts with the menu (**`#menu-btn`**),
  reload (**`#reload-tab`**) and copy link (**`#copy-page-link`**). The tabs (**`#strip`**,
  **`role="tablist"`**) immediately follow the page actions. Reload, like **`Ctrl`+`R`**, affects
  only the active frame. Copy uses that frame's current same-origin HTTP(S) URL, keeps its route
  and removes `desktop_workspace`, `desktop_workspace_pending` and URL credentials. The status
  appears below the buttons for two seconds. After the tabs come **`#new-tab`**, the drag area
  (**`#drag`**), scroll arrows (**`#scroll-btns`**, visible on overflow) and **`#window-btns`**
  (only for a driven window without system chrome). Native macOS moves the three standard buttons
  slightly inward in the unified strip without changing the menu anchor. Its popup aligns with
  the button. AppKit
  owns the window buttons, resize edges and corner radius. In the unified strip, green zooms
  without entering full screen; a separate system frame restores the window's original collection
  behavior. The empty bar and its padding drag the window.
  **`GET /desktop/window`** adds **`frame: "system"`** and **`left_inset`** for this optional
  capability; controls without it keep the ordinary **`supported`** answer. A tab is a button carrying the
  state dot of its chat, its label and a **`×`**; the one in front is marked by
  **`aria-selected`**, a lighter ground and a 2px rule along its bottom edge in
  **`--tab-active-line`**, and nothing else in the strip takes that colour. Dragging the empty
  space moves the window (on native macOS its padding does too) and a double click maximises or restores it, which is what a title bar
  does; a new chat is only the **`+`** or the chord.
- **A tab's name** is the title of the chat behind it, read from **`GET /coddy/sessions`** and
  written with **`textContent`**, never **`innerHTML`**: it is operator and model text drawn into a
  credentialed same-origin document. A chat that has not started is **`New chat`**. The list is read
  again every 10s, and once more 1.5s after a frame first reports a session, because a chat is named
  after its first message and that message is still being saved as the page learns the session
  exists.
- **A hidden tab is hidden, not unmounted** (**`visibility: hidden`**, **`z-index: -1`**, the frame
  kept in the DOM): the whole point of a tab is that the chat behind it keeps its transcript, its
  scroll position and its stream. Memory therefore grows with the tabs; the event streams do not,
  since every instance of one origin shares a single **`GET /coddy/events`**.
- **The chords are `Ctrl`**, with `Alt` beside every one of them: `Ctrl+T` opens a chat, `Ctrl+W`
  closes the one in front, `Ctrl+1`..`Ctrl+9` switch, and `Ctrl+Tab` / `Ctrl+Shift+Tab` walk the row
  and wrap at both ends, bringing the tab they reach into sight. Three of the keys never arrive as
  key events at all. A Chromium window opened with **`--app`** answers `Ctrl+T`, `Ctrl+W` and
  `Ctrl+Tab` itself - a new browser window, closing the application, and walking its own tabs - and
  a page cannot cancel any of them, so the launcher takes them
  at the X server with a passive grab on the window and the page asks for them over
  **`GET /desktop/keys`**, one long poll at a time (**`external/desktop/chords.go`**). `Ctrl+Tab`
  and `Ctrl+Shift+Tab` are **two grabs on one keycode**, because a passive grab matches the modifier
  state exactly and a grab on `Ctrl` alone would never see the second. The digits
  need none of that: an application window has no tabs of its own to switch to and hands them over.
  A key pressed inside a chat is delivered to that frame's document and never reaches the parent, so
  the handler is attached to each frame's **`contentDocument`** on every load - allowed because the
  frame is same-origin, and not cooperation from the SPA, which is unchanged.
- **The menu** (**`#menu`**) hangs under the button at the left edge and holds the window's own
  switches: **New chat**, **Open at login**, **Show the tray icon**, **Start minimised** and, while
  the window can be driven, **Show the system frame**. **The page draws no list of its own**: the
  rows come from **`GET /desktop/menu`**, decided by **`shell.MenuRows`** in Go, which is also what
  the tray icon's `com.canonical.dbusmenu` menu is built from - one list, two menus, and a row added
  to one appears on the other. The tray's carries two rows the bar's cannot, above a separator:
  **Show the window** and **Hide the window**. They are the launcher's, not the page's
  (**`GET`** / **`POST /desktop/settings`**, kept in **`<home>/desktop/settings.json`**), so a tick
  moves only once the launcher says the machine moved with it, and a switch that failed keeps its
  place and carries the reason on the row (**`.row.failed`**). An action closes the menu behind it;
  a switch leaves it open. The menu closes on a pointer going down anywhere outside it - the bar, a
  tab, its own button, the chat below (said again on each frame's **`contentDocument`**), another
  application (**`blur`**) - and on `Escape`; the button that opened it is no exception to that, and
  what keeps a click on it from reopening the menu on its way up is a 300ms guard, not an attempt
  to recognise the button under a pointer that is really over the path of its icon.
- **The state dot** in front of a tab's label is the mark the History list uses, read off the same
  **`GET /coddy/sessions`** row and in the same order (work, then a prompt waiting for a person,
  then a failure, then an answer nobody has seen): a pulsing filled dot while the chat works, a
  ring in the accent while it waits for you, a red dot for a failed turn, a filled accent dot for a
  chat that finished in another tab, a hollow ring otherwise. The tab in front carries neither an
  unread mark nor an unseen failure, because it is the one being read.
- **The window's corners** are cut out of the window itself while it floats and left square while
  it fills the screen (**`external/desktop/window_shape_x11.go`**), because a bare rectangle with
  square corners looks like a bug beside every other window. The page follows the same shape
  through **`data-window="floating" | "maximised"`** on **`<html>`**, read off the window's own
  size, so it never paints into a corner that was cut away.
- **The window always keeps a tab**: closing the last one opens an empty chat, since closing the
  window is what ends the application.
- **The theme** is read off the chat in front, which is same-origin: **`--text`**, **`--bg`**,
  **`--accent`**, **`--coddy-danger`** and the four tokens of the canvas the SPA actually paints its
  page with - **`--coddy-canvas-gradient-top`** / **`-bottom`** and the two glows. **The bar is
  painted with that same stack, at the same geometry** (**`html`** in **`shell.css`**), because it
  stands exactly where the top of that page would be; a flat **`--bg`** only matched in the light
  theme, where the top of the gradient happens to be the same white, and read as a foreign band in
  the other eight. Everything else the strip draws is derived from those values the way the SPA's
  **`mix-text()`** is, so a theme this page has never heard of reaches the bar, the tabs and the
  state dots. Before the first chat has loaded there is no frame to read, and the
  **`coddy_ui_theme`** cookie - scoped to the host rather than the port, which is why it reaches the
  shell whatever port the desktop took - stands in with light or dark and nothing finer.

### Installable app and notifications

The web UI installs as an app and shows system notifications while it is in the background (issue
#508). Everything of it lives in **`ui/pwa/`** and **`public/`**; the functional checklist is
**`docs/surfaces/web-ui.md`** (*Installing the web UI as an app*, *Notifications*), the scenarios
**`features/web_ui_installable_app.feature`**.

- **What the server serves.** **`public/manifest.webmanifest`** (id, start URL and scope **`/`**,
  **`display: standalone`**, the dark canvas **`#0f0f10`** as theme and background colour), the
  icons **`icon-192.png`** and **`icon-512.png`** (the flat mark on its rounded plate, transparent
  corners, purpose **`any`**) and **`icon-maskable-512.png`** (the full-bleed plate of
  **`coddy-logo-mark-icon.svg`**, the mark inside the safe zone, purpose **`maskable`**), and
  **`public/sw.js`**. Vite copies them into **`dist`**, **`scripts-sync-to-go.mjs`** next to
  **`embed.go`**, the **`Dockerfile`** stages them, and **`ui.Handler()`** serves them
  **`no-cache`**, the manifest as **`application/manifest+json`**. Rename them everywhere or
  nowhere.
- **The service worker caches nothing.** It has no **`fetch`** handler and no cache: every request
  still reaches the server, so a new build is never hidden behind an old copy and an offline page is
  the error a tab shows. Do not add caching to it without a design for invalidating it. It shows the
  notices tabs send it (**`coddy-notify`**) and opens a chat on a click (**`coddy-open`**).
  **`serviceWorker.ts`** registers it from **`main.tsx`** in a secure context only.
- **The title bar follows the theme.** **`themeColor.ts`** keeps **`<meta name="theme-color">`** on
  **`--coddy-canvas-gradient-top`** of the applied theme, so an installed window is not dark over a
  light theme.
- **The switch.** **Settings → Appearance**, under the language picker
  (**`NotificationsSetting`**, **`.appearance-notify-block`**): the section label, then one
  **`SwitchField`** (**`appearance-notifications-switch`**) whose description stays behind the
  **(i)**, then a status line (**`.appearance-notify-status`**, the **`settings-field-desc`** type)
  only when the switch cannot be turned on here - blocked by the browser, an insecure page, a browser
  without notifications - in which case the switch is disabled. Turning it on asks the browser for
  the permission inside that click; the choice is the cookie **`coddy_ui_notify`**, client-side like
  the theme. Not rendered inside the Telegram Mini App.
- **When a notice is shown.** Only while the page does not have the person's attention (hidden, or
  its window not focused) and, through the service worker, only while no window of the app has the
  focus. A tab announces only the chats it follows - the one on screen and the ones it sent a prompt
  to (**`AttentionTracker`**): the end of a turn (from **`turn_ended`**, worded from the turn
  stream's error when it failed), a permission request, a question, a background subagent's prompt
  that arrives live (never one the stream replays on connect).
- **One notice per occurrence.** The tag is **`coddy:<environment>:<session>:<kind>:<key>`**,
  where the key is what every tab hears alike: the server's time of the turn's end, the tool call's
  or the request's id. The service worker shows a tag once within 30 seconds.
- **What a notice says.** Title: the chat's title, else **Coddy**. Body: one line of at most 140
  characters in the page's language (**`notify.*`**), naming what happened; it never quotes the
  answer, since a notification lands on a lock screen. Icon: **`/icon-192.png`**.
- **A click** focuses the tab that asked (else any tab of the app, else a new window on
  **`/#/s/<id>`**) and opens the chat through the same path as a History pick, only when the notice
  belongs to the environment the tab shows.

### JavaScript extensions

Extensions observe app hash navigation (`tabChanged`), completed model calls
(`stepEnded`, the raw `token_usage` frame) and completed whole turns (`turnEnded`,
the app's shared server-events connection). Callbacks are observational and may
be asynchronous. A failing listener is removed; other listeners and the host
continue. No new global connection is opened for extensions.

`theme.get()` hands a frozen snapshot of the active appearance - the theme id,
its `light`/`dark` mode and the colours of the tokens an extension paints with
(`bg`, `text`, `muted`, `accent`, `danger`, `link`, `border`, `surface`),
resolved to computed colours through a probe element the way the browser
panel's colours are (`browserPanel/bridge.ts`). `theme.onChange` notifies on a
`data-theme` switch with the same delivery rules as the lifecycle events:
frozen copies, by extension id in ascending ASCII order then registration
order, a failing listener removed and reported in Settings, everything disposed
by the lifetime signal. Extension styles run in the app's document and take the
theme's CSS variables directly. Contract: `extensions/theme.test.ts`,
`features/ui_extension_theme.feature`.

`ui.registerToolRenderer` selects an MCP call by exact server/tool names or
wildcards, with specificity, extension id and registration order as precedence.
The extension mounts its own DOM and may update it as the call changes. The host
keeps Original tool details underneath; prompts stay host-owned. Errors remove
the renderer and restore the next match or the normal card. Disposal follows
the extension and the transcript window's mounted rows.

`settings.registerField` appends typed scalar controls to existing Settings
sections, grouped by extension id, through the ordinary form and its draft.
Values persist under `extension_values.<extension-id>.<field-id>` in config.yaml.
`settings.get` reads saved values or registered defaults; `settings.set` uses
the ordinary authenticated, revision-aware config transaction and cache refresh.
Disabling an extension removes controls while retaining their saved values.

`coddy.net.fetch(url, options)` sends buffered HTTP(S) requests from the selected
Coddy server, including loopback and private targets. The SDK uses the normal
authenticated environment route and the exact enabled package digest. Target
credentials are explicit; browser cookies and Coddy's bearer stay on the outer
request. JSON carries target status, headers and base64 bytes, so target HTML
and cookies do not become responses of the application origin. Redirects are
returned without following them. Calls are bounded to 30 seconds, 4 MiB upload
and 8 MiB download. The server cancels in-flight calls on disable/removal through
its routes; the SDK also binds them to its lifetime and the caller's signal.
Settings' risk warning names access to local and external services from the server.
Incoming `transform` callbacks stay synchronous; network data can feed a cache
read by those callbacks. See the feature guide for the complete network contract.

The Extensions entry of the navigation opens Settings → Extensions before any
extension contributes controls. With contributions, it offers a native select
of registered pages and menu actions plus Manage extensions. On a phone it
folds into More with the other optional entries.

Settings → Extensions is a synthetic, schema-independent section on agents.
It shows the risk statement, Install ZIP and Refresh, then package cards with
name, version, description, enabled state, SHA-256, switch and remove actions.
Enabling uses the shared confirmation dialog and reloads the page. Installation
always leaves the package disabled. The host stores packages; no settings live
in the YAML form's draft. The section is searchable by its tab title.

Extension pages mount into a native modal dialog with a title, Close and an
extension-owned body. The dialog traps focus and answers Escape. Styles use
theme tokens. Registrations have update/remove handles, all ids are namespaced,
and the lifetime owns an abort signal. Disabling, removing or changing evaluated
code requires a full reload; environment switches do the same after evaluation.
The implementation and initial API are in ui/extensions; the user contract is
docs/features/extensions.md. The examples live in the companion repository [coddy-project/coddy-extentions](https://github.com/coddy-project/coddy-extentions). This phase adds pages, menu actions and incoming
turn-stream processors from the [expanded extension proposal](https://gist.github.com/hijera/95b016e1721608e8c3974fe3b80e29dc).

`coddy.stream.registerIncoming({id, transform})` registers a synchronous payload
processor before the turn reader applies an SSE frame. Both the POST reader and
the composer relay reader use this chain, including the EOF tail. The callback
receives the data string and frozen context (`sessionId`, `transport`, `event`,
`id`, optional `ageMs`). It returns a replacement string, or `undefined` to observe
without replacing. JSON input requires JSON output. A processor can enrich JSON,
rewrite assistant deltas or tool results, and keep state in its extension.
The reader waits up to 11 seconds for initial extension loading, then continues
with the registered processors if loading fails or exceeds that bound. Extensions
that activate later do not receive frames already consumed.

Processors run by extension id in ascending ASCII order, then registration order
within an extension, so parallel module activation cannot reorder the chain.
Each sees its predecessor's output. Throws, asynchronous callbacks and invalid
results remove that processor, show the error in Settings → Extensions and pass
its input to the next processor. Processing does not await extension promises;
callbacks must finish promptly on the browser thread. A remove handle or the
extension's abort signal revokes a registration before the next frame.

The callback cannot replace transport metadata or generate `[DONE]`; the existing
`[DONE]`, `desync` and frames without data bypass processors. This preserves the reader's cursor
and termination. Payload schemas still apply, including tool ids, permission ids
and versions the host uses. The SDK is not a sandbox for approved origin code.
Transforms affect live browser consumption only; REST history reconciliation can
replace them with the server's original data. They do not change stored history,
LLM context, outgoing requests or the shared `/coddy/events` connection. There is
no second events reader and no interception of authentication or asset fetches.

For assistant text, `coddy.stream.transformText({id, transform})` is the stable
semantic alternative: it decodes supported text-delta shapes and leaves every
other frame and payload field alone. Raw `registerIncoming` remains the explicit
escape hatch. `required_features` and `engines.coddy` are checked before module
import, and `coddy.host.info()` exposes the same feature contract to the package.

Composer changes are targeted by default: `after` and `before` place one control
beside one stable key without reordering the host's other controls, and
`settings.renameRow` retitles one row without moving the rest. Full-list alters
live under `coddy.experimental`; `resetComposerControls()` is the separate
explicit takeover. Hook consumers call `hooks.invoke(ref, payload)` instead of
querying the slot owner's DOM. Providers are upserted by `name` and models by
`model` in one configuration transaction. Theme colour parsing and formatting
live in `coddy.theme`, not in each extension.
