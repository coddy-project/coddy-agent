export type { ExtensionAPI as Coddy } from "./runtime";
export type { ExtensionHostInfo } from "./hostCompatibility";
export type { ComposerControl } from "./composerControls";
export type {
  IncomingRegistration,
  IncomingTextRegistration,
} from "./incomingStream";
export type { ExtensionThemeAPI, ThemeHSL } from "./theme";
export type { ProviderUpsert } from "./providers";
export type { ExtensionSettingsAPI } from "./settings";

export type CoddyManifest = {
  api: 1;
  id: string;
  name: string;
  version: string;
  description?: string;
  permissions?: readonly string[];
  required_features?: readonly string[];
  engines?: { coddy?: string };
  ui: {
    entry: string;
    styles?: readonly string[];
    compat_styles?: readonly string[];
  };
  themes?: readonly {
    id: string;
    name?: string;
    title?: string;
    mode?: "light" | "dark";
    styles: readonly string[];
    swatch?: Record<string, string>;
  }[];
};
