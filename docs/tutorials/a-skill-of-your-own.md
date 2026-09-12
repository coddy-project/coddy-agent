# A skill of your own

A skill is a `SKILL.md` file with a frontmatter and a body of instructions; it becomes a slash command on every surface and its body reaches the model only when invoked. The reference is [Skills](../features/skills.md), in particular [Writing your own skill](../features/skills.md#writing-your-own-skill) and [Supported file formats](../features/skills.md#supported-file-formats).

1. **Create the pack.** One skill per directory under `~/.coddy/skills/`; the directory name is the slash command, so keep `name` equal to it. `description` is the line the catalog and the Settings page show, and an optional `version` is shown by `coddy skills list` and used to detect updates for skills installed from a source.

   ```markdown
   ---
   name: release-notes
   description: Turns the commits since the last tag into release notes grouped by area.
   version: 1.0.0
   ---

   # Release notes

   Run `git describe --tags --abbrev=0` to find the last tag, then `git log <tag>..HEAD --oneline`.
   Group the commits by scope, drop merge commits, write one line per change in the imperative
   mood, and end with a "Breaking changes" section, or "None" when there are none.
   ```

   Saved as `~/.coddy/skills/release-notes/SKILL.md`. The other two default directories are `~/.agents/skills/` (global, shared with `npx skills` and other agents) and `${CWD}/.coddy/skills/` (project-local, the highest priority); a later directory wins when two skills share a name, and any other folder can be added to `skills.dirs` in `config.yaml`.

2. **See it in the catalog.** `coddy skills list` prints the search roots and a table with the skill, its version, `enabled` or `disabled`, and the description.

   ```bash
   coddy skills list
   ```

3. **Find it as a slash command.** Type `/` on the first line of the console editor and the suggestions include `/release-notes`; the web UI's composer lists it from `GET /coddy/slash-commands`, and an ACP editor receives it in `available_commands_update` after `session/new`. When a message contains `/release-notes`, the body is prepended to that message for the model under `## Invoked skill: /release-notes`, for that request only: the transcript keeps the message as you typed it. With `skills.auto_discovery` left at its default the model can also pull the skill in on its own through the `load_skill` tool when a request matches the description.

4. **Test it.** The loader rescans `skills.dirs` on every prompt, so edit, save and send again; no restart is involved. Print mode gives a quick loop from a repository with tags:

   ```bash
   cd ~/src/my-project
   coddy -p "/release-notes for everything since the last tag"
   ```

5. **Switch it off without deleting it.** `coddy skills disable release-notes` keeps the files and drops the command; `coddy skills enable release-notes` brings it back. The state is one name per line in `~/.coddy/skills/.disabled`.

   ```bash
   coddy skills disable release-notes
   coddy skills enable release-notes
   ```

6. **Share it.** Publish the directory in a GitHub repository. Others install it with `coddy skills add owner/repo` followed by `coddy skills sync`, or through `coddy plugin install owner/repo`, and a listing on [skills.sh](https://skills.sh) makes it findable with `npx skills find`.
