# Coddy canvases

Canvases referenced from issues and pull requests: mockups and prototypes drawn as HTML pages, kept together with the board that shows them. Nothing here is part of the documentation.

The `screenshots` branch keeps the pictures, PNG only. This branch keeps the pages behind them, so a mockup opens live in Coddy: every page runs as a frame of the board at the size the board gives it, and the agent changes it on request.

## Layout

An archive sits in the directory of its issue or pull request, named as on `screenshots` (`issue-<N>/`, `pr-<N>/`, or `issue-<slug>/` for an issue not filed yet), and every revision goes into a new `round-K/` directory. An archive once pushed is never overwritten: GitHub caches raw addresses.

```
issue-kiosk/mockups/round-1/kiosk-mockups-round-1.tar.gz
```

`<name>.tar.gz` unpacks into one folder, `<name>/`, which is the workspace of the board:

```
<name>/
  canvas/<board>.canvas   JSON Canvas 1.0, written as Coddy and Obsidian write it
  <board>/*.html          the pages, in a folder of their own
```

A file node names its page relative to that folder, so the board works with the folder as the chat's workspace.

## Opening one

```bash
curl -LO https://raw.githubusercontent.com/coddy-project/coddy-agent/canvases/issue-kiosk/mockups/round-1/kiosk-mockups-round-1.tar.gz
tar -xzf kiosk-mockups-round-1.tar.gz
```

Start a chat of the web UI in the unpacked folder (**Open folder…** in the folder pick over the composer, or `coddy serve` started in it) and open `canvas/<board>.canvas` in the Files window. The frames run in the web UI only: [Canvas](https://coddy.dev/docs/features/canvas).

## Adding one

- The pages are self-contained: nothing loaded from outside their folder, neutral data, no host names, tokens, user names or personal paths.
- A board holds at most 24 pages and a page at most 10 MB. No symbolic links and no dot-named files or folders: a frame serves neither.
- Pack it so that the same files give the same bytes:

  ```bash
  tar --sort=name --mtime='YYYY-MM-DD 00:00:00Z' --owner=0 --group=0 --numeric-owner \
    --format=ustar -cf - <name> | gzip -9n > <name>.tar.gz
  ```

- Link it from the issue or pull request by its raw address, next to its screenshots: `https://raw.githubusercontent.com/coddy-project/coddy-agent/canvases/<path>`.
