import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import React from "react";
import { WorkspaceFolderModal } from "./WorkspaceFolderModal";
import { setLocale } from "../i18n/i18n";

const rootListing = {
  path: "/workspace",
  parent: "/",
  folders: [
    { name: "visible", path: "/workspace/visible" },
    {
      name: "directory-link",
      path: "/workspace/directory-link",
      symlink: true,
      target: "/targets/project",
    },
  ],
};

function jsonResponse(body: unknown) {
  return Promise.resolve(new Response(JSON.stringify(body), { status: 200 }));
}

function renderModal() {
  const props = {
    open: true,
    startPath: "/workspace",
    onClose: vi.fn(),
    onPick: vi.fn(),
  };
  render(<WorkspaceFolderModal {...props} />);
  return props;
}

beforeEach(() => {
  setLocale("en");
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  setLocale("en");
});

describe("WorkspaceFolderModal", () => {
  it("reloads with show_hidden and mutes hidden folders when requested", async () => {
    const fetch = vi.fn((url: string) => {
      if (url.includes("show_hidden=true")) {
        return jsonResponse({
          ...rootListing,
          folders: [
            ...rootListing.folders,
            { name: ".private", path: "/workspace/.private", hidden: true },
          ],
        });
      }
      return jsonResponse(rootListing);
    });
    vi.stubGlobal("fetch", fetch);
    renderModal();

    await screen.findByTestId("workspace-modal-row-visible");
    const toggle = screen.getByTestId("workspace-modal-show-hidden");
    const actions = screen.getByTestId("workspace-modal-actions");
    expect(actions).toContainElement(toggle.closest("label"));
    expect(actions.firstElementChild).toBe(
      screen.getByTestId("workspace-modal-new-folder"),
    );
    expect(actions.children[1]).toBe(toggle.closest("label"));
    expect(toggle).toHaveAttribute("role", "switch");
    expect(toggle).toHaveAttribute("aria-checked", "false");
    // The scheduler's plus glyph on its colours (shared CSS rules), not a "+"
    // typed into a text button.
    expect(
      screen.getByTestId("workspace-modal-new-folder").querySelector("svg"),
    ).not.toBeNull();
    expect(screen.getByTestId("workspace-modal-new-folder")).toHaveClass(
      "workspace-modal-btn--add",
    );
    expect(screen.getByTestId("workspace-modal-new-folder")).toHaveAttribute(
      "title",
      "New folder",
    );
    fireEvent.click(toggle);

    await screen.findByTestId("workspace-modal-row-.private");
    expect(fetch).toHaveBeenLastCalledWith(
      "/coddy/workspace/folders?path=%2Fworkspace&show_hidden=true",
    );
    expect(
      screen.getByTestId("workspace-modal-row-.private").className,
    ).toContain("workspace-modal-row--hidden");
  });

  it("labels a directory symlink with its target and follows its path", async () => {
    const fetch = vi.fn((url: string) => {
      if (url.includes("directory-link")) {
        return jsonResponse({
          path: "/targets/project",
          parent: "/targets",
          folders: [],
        });
      }
      return jsonResponse(rootListing);
    });
    vi.stubGlobal("fetch", fetch);
    renderModal();

    const row = await screen.findByTestId("workspace-modal-row-directory-link");
    expect(row).toHaveTextContent("directory-link");
    expect(row).toHaveTextContent("→ /targets/project");
    // A link wears the folder glyph with an arrow cut out of it; a plain folder
    // does not.
    expect(
      row.querySelector(".workspace-modal-symlink-icon svg"),
    ).not.toBeNull();
    expect(
      screen
        .getByTestId("workspace-modal-row-visible")
        .querySelector(".workspace-modal-symlink-icon"),
    ).toBeNull();
    fireEvent.click(row);

    await waitFor(() =>
      expect(fetch).toHaveBeenLastCalledWith(
        "/coddy/workspace/folders?path=%2Fworkspace%2Fdirectory-link",
      ),
    );
    await waitFor(() =>
      expect(screen.getByTestId("workspace-modal-path")).toHaveValue(
        "/targets/project",
      ),
    );
  });
});
