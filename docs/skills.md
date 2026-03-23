# Skills and Cursor Rules

## Overview

The agent can read skills and cursor rules from the filesystem, exactly like Cursor IDE does.
These files provide context, instructions, and domain knowledge that are injected into the
system prompt when relevant.

## Supported File Types

### 1. Cursor Rules (`.cursor/rules/*.md` or `.cursor/rules/*.mdc`)

Standard Cursor rule files. Located in the project's `.cursor/rules/` directory.

Format:
```markdown
---
description: "Short description of when this rule applies"
globs: ["**/*.go", "**/*.mod"]
alwaysApply: false
---

# Rule Title

Content of the rule. Markdown format.
Write code comments in English.
Use error wrapping with fmt.Errorf("context: %w", err).
```

Frontmatter fields:
- `description` - human-readable description
- `globs` - list of file patterns. Rule is applied when any matched file is in context
- `alwaysApply` - if true, always inject regardless of context

### 2. Agent Skills (`SKILL.md`)

Skill files provide reusable instructions for specific tasks. Compatible with Cursor skills format.

Format:
```markdown
# Skill Title

Short description of what this skill does.

## Instructions

Detailed instructions...
```

Skills are discovered by searching for `SKILL.md` files in the configured skill directories.

### 3. Plain Markdown Rules

Simple markdown files placed in `.cursor/rules/` without frontmatter are treated as
always-apply rules.

## Loading Priority

Skills and rules are loaded from multiple sources, in priority order (first wins):

1. Files in `${WORKSPACE}/.cursor/rules/` - project-level rules
2. Files in `${WORKSPACE}/.cursor/skills/` - project-level skills
3. Files in `~/.cursor/skills/` - user-level global skills
4. Files in `~/.cursor/skills-cursor/` - cursor-specific skills
5. Extra files specified in `config.yaml` under `skills.extra_files`

## How Rules Are Applied

When processing a `session/prompt`, the agent:

1. Collects all skill/rule files from configured directories
2. Filters based on `globs` matching files mentioned in the prompt context
3. Includes all `alwaysApply: true` rules
4. Builds a combined system prompt prefix with all applicable rules

## Example Rule File

`.cursor/rules/go-standards.md`:
```markdown
---
description: "Go coding standards for this project"
globs: ["**/*.go"]
alwaysApply: false
---

# Go Coding Standards

- Write all code comments in English
- Use `fmt.Errorf("context: %w", err)` for error wrapping
- Prefer early returns over nested if-else
- All exported functions must have godoc comments
- Use table-driven tests with `t.Run`
- Never use `panic` in library code
```

## Example Skill File

`~/.cursor/skills/code-review/SKILL.md`:
```markdown
# Code Review Skill

Provides guidance for conducting thorough code reviews.

## Instructions

When asked to review code:
1. Check for security vulnerabilities
2. Verify error handling is complete
3. Look for performance issues
4. Check test coverage
5. Verify documentation is adequate
```

## Adding Custom Skills at Runtime

Users can add skills via the session's MCP server configuration. The agent
exposes a built-in MCP-compatible tool `list_skills` that returns loaded skills,
and skills can also be provided via MCP resource URIs.

Alternatively, additional skill directories can be configured in `config.yaml`:
```yaml
skills:
  dirs:
    - "~/my-custom-skills"
    - "/shared/team-skills"
```
