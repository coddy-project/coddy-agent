/**
 * Version of the extension theme-token contract. Bump this whenever the token
 * list changes so extensions can tell which SDK shape the host provides.
 */
export const THEME_TOKEN_CONTRACT_VERSION = 1 as const;

/**
 * The theme tokens an extension may read and override. This list is generated
 * from the seven palettes in src/styles/themes/ and shared src/styles/_tokens.scss,
 * then committed as source: runtime stylesheet discovery is not part of the SDK.
 */
export const EXTENSION_THEME_TOKENS = Object.freeze([
  "--accent",
  "--bg",
  "--bubble-user",
  "--coddy-blend-base",
  "--coddy-bubble-user-bg",
  "--coddy-bubble-user-fg",
  "--coddy-canvas-glow-indigo",
  "--coddy-canvas-glow-purple",
  "--coddy-canvas-gradient-bottom",
  "--coddy-canvas-gradient-top",
  "--coddy-chat-header-bg",
  "--coddy-chip-badge-bg",
  "--coddy-chip-badge-fg",
  "--coddy-composer-wrap-pad",
  "--coddy-context-ring-fg",
  "--coddy-context-ring-inner",
  "--coddy-danger",
  "--coddy-ghost-btn-fg",
  "--coddy-glass-panel-backdrop",
  "--coddy-glass-panel-bg",
  "--coddy-glass-panel-border",
  "--coddy-glass-panel-radius",
  "--coddy-glass-panel-shadow",
  "--coddy-grain",
  "--coddy-hero-accent",
  "--coddy-hero-accent-glow",
  "--coddy-hero-muted-end",
  "--coddy-hero-muted-mid",
  "--coddy-link",
  "--coddy-link-action",
  "--coddy-link-action-hover",
  "--coddy-md-inline-code-bg",
  "--coddy-md-inline-code-fg",
  "--coddy-md-inline-code-font",
  "--coddy-on-accent-fg",
  "--coddy-overlay-scrim-bg",
  "--coddy-plan-eye-bg",
  "--coddy-plan-eye-fg",
  "--coddy-skill-chip-bd",
  "--coddy-skill-chip-bg",
  "--coddy-skill-chip-font",
  "--coddy-skill-chip-text",
  "--coddy-surface-code-bg",
  "--coddy-surface-code-fg",
  "--coddy-surface-field",
  "--coddy-surface-fieldset",
  "--coddy-surface-gutter",
  "--coddy-surface-inset",
  "--coddy-surface-muted",
  "--coddy-surface-raised",
  "--coddy-surface-subtle",
  "--coddy-surface-sunken",
  "--coddy-tip-bg",
  "--coddy-tip-border",
  "--coddy-tip-fg",
  "--coddy-tip-shadow",
  "--coddy-todo-completed",
  "--coddy-top-rhythm",
  "--coddy-z-slash-command",
  "--diff-add-bg",
  "--diff-add-no",
  "--diff-del-bg",
  "--diff-del-no",
  "--diff-hunk-bg",
  "--muted",
  "--nav",
  "--nav-floating-gutter",
  "--radius",
  "--rail-column-pad-end",
  "--rail-pill-pad-inline",
  "--rail-pill-track",
  "--shadow",
  "--sidebar",
  "--syntax-attribute",
  "--syntax-comment",
  "--syntax-deletion",
  "--syntax-keyword",
  "--syntax-meta",
  "--syntax-number",
  "--syntax-string",
  "--syntax-title",
  "--syntax-type",
  "--text",
] as const);

export type ExtensionThemeToken = (typeof EXTENSION_THEME_TOKENS)[number];

const contractTokens: ReadonlySet<string> = new Set(EXTENSION_THEME_TOKENS);

export function isExtensionThemeToken(
  name: string,
): name is ExtensionThemeToken {
  return contractTokens.has(name);
}
