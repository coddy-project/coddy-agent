# Project-local MCP trust in other agents

Research record behind issue #80 and the workspace trust gate (`internal/mcp/trust.go`,
`internal/mcp/gate.go`, policy `mcp.project_trust`). It records how three other agents gate an MCP
server declared inside a checkout, verified on 2026-08-02 against locally installed builds rather
than their documentation.

## Claude Code 2.1.212

A project `.mcp.json` is gated by the folder trust dialog plus a per-server prompt ("New MCP server
found in this project"). The decision is stored as a bare server **name** in
`enabledMcpjsonServers` / `disabledMcpjsonServers` / `enableAllProjectMcpServers` of
`settings.local.json`, so a command swapped under an approved name runs again without a prompt. The
dialog shows the name only, never the command. `claude mcp reset-project-choices` resets the
choices. The behaviour is described publicly at
https://repello.ai/blog/claude-code-mcp-name-keyed-trust and is considered working as designed by
the vendor.

## Codex CLI 0.144.4

A project `.codex/config.toml`, which may declare `mcp_servers`, is read only for a trusted project;
trust is `[projects."<path>"] trust_level = "trusted"` in `~/.codex/config.toml`, and project keys
Codex does not support there are dropped with a warning. There is no per-server prompt and no digest
for MCP, although hooks carry a `trusted_hash` (with `--dangerously-bypass-hook-trust` to skip it).

## Cursor 3.13.25 and cursor-agent 2026.07.23

The strictest of the three. A server is enabled only by an approval key
`identifier:hash(command, args, env, envFile, url, headers)`; editing the declaration prunes the
stale key and prompts again. This is the design that followed MCPoison (CVE-2025-54136, fixed in
Cursor 1.3). The headless CLI enforces the same through `approvalService.isServerApproved` and
`McpServerNotApprovedError`, with approvals in a per-project `mcp-approvals.json`.

## What it means for Coddy

Coddy binds an approval to the digest of the declaration, as Cursor does, so a changed command is a
new decision rather than a silent rerun. None of the three shows the effective command - after
`${NAME}` and `${CWD}` are resolved - before the approval, and none names the environment variables
a declaration reads; that is where Coddy's approval surfaces (`ReadsEnvironment`) go further.
