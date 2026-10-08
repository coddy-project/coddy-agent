# Images

A model that reads images gets a picture two ways: you attach it to a prompt, or the agent opens an image file of the workspace with `read`. This page covers the second way and where you see what the agent looked at. Attaching a picture in the web composer is described in [Web UI](../surfaces/web-ui.md#composer-file-attachments-multimodal) and, for a message sent while a turn runs, in [Message queue](message-queue.md).

## A model that reads images

Pictures reach only a model marked as one that takes them, with `multimodal: true` on its row of `models`:

```yaml
models:
  - model: "openai/gpt-5.6-terra"
    multimodal: true
```

Every provider type sends them: an OpenAI-compatible endpoint as `image_url` parts, Anthropic as image blocks, the Codex backend as `input_image` parts, Devin with the prompt, a model a remote Coddy shares as image parts of its own wire that the remote hands to its provider (a refusal by that provider comes back as an error). An SVG attached to a prompt is text, and every provider gets it as text. Anthropic, the Codex backend and Devin take PNG, JPEG, GIF and WebP; a picture of another type is named in the message as not sent rather than failing the request. An OpenAI-compatible endpoint is sent a picture of any type, since a local server behind that API may take more types than OpenAI does. Mark a model this way only when it really accepts images; a text-only endpoint refuses a request that carries one. A model of a `type: coddy` provider (a model another Coddy shares) needs no key: with `multimodal` left out, the remote's listing says whether the model reads images, and a written value, `false` included, wins ([Capabilities from the listing](shared-models.md#capabilities-from-the-listing)).

## Reading an image file

`read` tells a picture by its content, never by its name: a PNG, JPEG, GIF or WebP file is an image even without an extension, and a text file named `shot.png` is read as text. For a model that reads images, `read` answers with one line naming the file, its type, size in pixels and bytes, and the model is shown the picture itself:

```text
screenshots/after.png: PNG image, 1280x720, 45.2 KB. The picture is attached for you to look at.
```

Use it the way you would ask a person to look: check a screenshot the tests just saved, compare a rendered page before and after a change, read a diagram. Several images read in one step reach the model together.

A picture is refused, with the reason, when:

- the session's model does not read images - a model switch in the middle of a turn counts, since the check is made when the file is read; this reason comes before any other, so the model is not told how to make a file smaller that it could not see anyway;
- the file is larger than 3.75 MB, which base64 encoding turns into the 5 MB one Anthropic request takes per image; the size on disk decides, before the file is loaded;
- a side is longer than 8000 pixels;
- the file ends before its image does - a screenshot still being written, a download cut short: a PNG is followed to its `IEND` chunk, a JPEG to its end-of-image marker, a WebP chunk by chunk to its image data, and a WebP whose header states a size other than what follows it is refused too, as libwebp refuses it;
- the file is all there but holds no image - a PNG with no image data, a JPEG with no scan - or a chunk of a PNG does not match its checksum;
- it is an animated WebP, which not every provider takes;
- the content looks like an image but cannot be decoded.

A picture the history kept although a provider refuses it would fail every later request of the session, which is why these are refused up front. The agent can save a scaled-down copy, or a frame, with a command and read that. A GIF is shown as its first frame, as a PNG: every provider takes that, and the rest of an animation is never decoded. Any other binary file - a PDF, an archive - is refused with its type and size, as before.

## What the model is sent

The picture stays with the result of the `read` that produced it. The transcript gains no extra message, so a reload, `/resume` in the console or an editor that loads the session shows the conversation as it was typed.

What goes to the provider is built from that transcript on every request. An OpenAI-compatible tool result cannot hold an image, and nothing may come between the results of one step, so the pictures of a step travel in one user message right after the step's tool results, named in the order they were read. The message is rebuilt the same way each time, so the provider's prompt cache keeps working.

A picture costs context like anything else the model reads, and it goes out with every request after the step that read it. A request carries at most 20 pictures and 20 MB of them, the pictures attached to prompts and the pictures of tool results together, newest first, a picture too large for the bytes left giving way to an older one that fits. A text file attached to a prompt is no picture and takes no place among them. That keeps a session of many screenshots within what a provider takes (the Anthropic API refuses a request over 32 MB, and wants pictures of at most 2000 pixels a side once a request holds more than 20); a picture left out is named where it was, in the note of its step or in its own prompt. When [result eviction](compaction.md#result-eviction) collapses an old `read`, its picture goes too. Either way the model reads the file again if it needs it. The context estimate counts every picture a request carries as about 1600 tokens, the same pictures the request is built with, so eviction, [automatic compaction](compaction.md) and the context ring see a session of screenshots for what it costs, and a model without `multimodal` is charged for none.

A session switched to a model that does not read images (no `multimodal: true`, or a `coddy` model the remote does not list as multimodal) sends no picture at all, prompt attachments included, and the model is told which pictures the steps and the prompts came with; a text file attached to a prompt still goes, written out in the message.

Coddy keeps one copy of every picture it showed the model, with the session's assets (`~/.coddy/sessions/<id>/assets/`), under the file's name plus a digest of its content. The result of the `read` names that copy, its type and size; the base64 the provider needs is built from the copy when a request goes out, so the transcript and the memory of a running session hold no second copy of the picture, and a picture read again unchanged is stored once. The copy is looked up by its name in the session's own assets directory, so a session directory that moved keeps its pictures, and it is sent only as it was saved: a copy that is gone, replaced by a link or holding other bytes than its digest names takes no place in the request, and the model is told the copy is gone and reads the file again. The surfaces show the same copy, so a screenshot overwritten a minute later still previews as the model saw it.

## Where you see it

![A read of screenshot.png in the web UI: the picture previewed under the row, then the answer](../assets/read-image-preview-dark-1280.png)

*The picture a `read` showed the model, previewed under its row; a click opens it enlarged.*

| Surface | What it shows |
|---|---|
| [Web UI](../surfaces/web-ui.md) | A preview card under the `read` row; a click opens the picture enlarged. It works for a session started in the console or in Telegram, and through a remote server or a relay. |
| [Telegram](../surfaces/gateway.md) | The bot sends the picture into the chat as a photo, captioned with the file's name, while the turn runs; as a document when Telegram refuses the photo itself. |
| [Console](../surfaces/console.md) | The `read` row with its one line of text. |
| [Editors](../surfaces/editors.md) | The `read` call with its one line of text. |
