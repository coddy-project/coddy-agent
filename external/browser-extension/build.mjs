import { mkdir, copyFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { zipDirectory } from "./zip.mjs";
const root = fileURLToPath(new URL(".", import.meta.url));
const common = {
  manifest_version: 3,
  name: "Coddy Browser",
  version: "0.1.0",
  description:
    "Connect selected browser tabs directly to your own Coddy server.",
  permissions: [
    "tabs",
    "storage",
    "scripting",
    "activeTab",
    "alarms",
    "notifications",
  ],
  host_permissions: ["http://*/*", "https://*/*"],
  action: { default_title: "Coddy", default_popup: "panel.html" },
  options_ui: { page: "panel.html", open_in_tab: true },
  // Firefox MV3 upgrades ws:// to wss:// by default. This explicit policy
  // keeps loopback WS working while scripts remain packaged-only. serverURL
  // separately requires TLS for every remote Coddy endpoint.
  content_security_policy: {
    extension_pages: "script-src 'self'; object-src 'self'",
  },
};
for (const browser of ["chrome", "firefox", "yandex"]) {
  const manifest = structuredClone(common);
  if (browser !== "firefox") {
    manifest.minimum_chrome_version = "118";
    manifest.permissions.push("debugger");
    manifest.background = { service_worker: "background.js", type: "module" };
    if (browser === "chrome") {
      manifest.permissions.push("sidePanel");
      manifest.side_panel = { default_path: "panel.html" };
    }
  } else {
    manifest.host_permissions = ["<all_urls>"];
    manifest.permissions.push("nativeMessaging");
    manifest.browser_specific_settings = {
      gecko: {
        id: "browser@coddy.dev",
        strict_min_version: "128.0",
        data_collection_permissions: {
          required: ["websiteContent", "authenticationInfo"],
        },
      },
    };
    manifest.background = { scripts: ["background.js"], type: "module" };
    manifest.sidebar_action = {
      default_title: "Coddy",
      default_panel: "panel.html",
    };
  }
  const out = `${root}dist/${browser}`;
  await mkdir(out, { recursive: true });
  await writeFile(
    `${out}/manifest.json`,
    JSON.stringify(manifest, null, 2) + "\n",
  );
  for (const file of [
    "background.js",
    "policy.js",
    "page.js",
    "input.js",
    "sse.js",
    "panel.html",
    "panel.js",
    "panel.css",
  ])
    await copyFile(`${root}${file}`, `${out}/${file}`);
  // The notification icon is the shared logo asset, copied under the name
  // background.js passes to notifications.create. A checked-in placeholder
  // path would ship as a text file on a checkout without symlink privilege.
  await copyFile(
    fileURLToPath(
      new URL("../../docs/assets/coddy-logo-mark-icon.svg", import.meta.url),
    ),
    `${out}/icon.svg`,
  );
  await zipDirectory(
    out,
    `${root}dist/coddy-${browser}.${browser === "firefox" ? "xpi" : "zip"}`,
  );
  console.log(`${browser}: ${out}`);
}
