import { expect, test } from "vitest";

import {
  alignedTranscriptSuffix,
  parseTranscriptWindow,
  prependOlderPage,
  transcriptPageQuery,
  transcriptToolCallsQuery,
} from "./transcriptWindow";
import type { TranscriptItem } from "./types";

const user = (id: string, content: string): TranscriptItem => ({
  id,
  type: "user_message",
  content,
});

test("a server that predates paged reads is read as a whole history", () => {
  expect(parseTranscriptWindow(undefined, 5)).toEqual({
    offset: 0,
    total: 5,
    turnsBefore: 0,
    userRowsBefore: 0,
  });
  // A window that cannot hold the messages it came with is not trusted.
  expect(parseTranscriptWindow({ offset: 4, total: 5 }, 3).offset).toBe(0);
  expect(
    parseTranscriptWindow(
      { offset: 3240, total: 3306, turnsBefore: 374, userRowsBefore: 376 },
      66,
    ),
  ).toEqual({ offset: 3240, total: 3306, turnsBefore: 374, userRowsBefore: 376 });
});

test("each read names its page in the query", () => {
  expect(transcriptPageQuery({ kind: "tail" })).toBe("?limit=60");
  expect(transcriptPageQuery({ kind: "from", offset: 3240 })).toBe("?from=3240");
  expect(transcriptPageQuery({ kind: "older", before: 3240 })).toBe(
    "?limit=80&before=3240",
  );
  expect(transcriptPageQuery({ kind: "full" })).toBe("");
  const w = { offset: 3240, total: 3306, turnsBefore: 0, userRowsBefore: 0 };
  expect(transcriptToolCallsQuery(w, 66)).toBe("?from=3240&to=3306");
  // The whole history asks for every call, as before.
  expect(
    transcriptToolCallsQuery({ ...w, offset: 0, total: 20 }, 20),
  ).toBe("");
});

test("an older page goes in front and never repeats an id on screen", () => {
  let n = 0;
  const newId = (p: string) => `${p}_${++n}`;
  const page = [user("u_1", "one"), user("u_2", "two")];
  const held = [user("u_3", "three")];
  // A lookalike an id preserving merge borrowed sits in the live window.
  const live = [user("u_2", "live")];
  const out = prependOlderPage(page, held, live, newId);
  expect(out.map((it) => it.id)).toEqual(["u_1", "row_1", "u_3"]);
  expect(out.map((it) => (it as { content: string }).content)).toEqual([
    "one",
    "two",
    "three",
  ]);
});

test("rows are carried over from a window that ends where the new one does", () => {
  const next = [user("u_3", "three"), user("u_4", "four")];
  const previous = [user("a", "one"), user("b", "two"), user("c", "three"), user("d", "four")];
  expect(alignedTranscriptSuffix(next, previous)?.map((it) => it.id)).toEqual([
    "c",
    "d",
  ]);
  // A different end is not the same rows.
  expect(
    alignedTranscriptSuffix(next, [...previous, user("e", "five")]),
  ).toBeUndefined();
  expect(alignedTranscriptSuffix(next, undefined)).toBeUndefined();
});
