import { t } from "../i18n/i18n";
import type { CoddyPermissionOption } from "./permissionTypes";

/**
 * Localizes a permission button.
 *
 * The backend authors option names in English (`internal/permission.Options`)
 * and they arrive verbatim over SSE, so translating has to happen here, keyed by
 * the stable `optionId` rather than by the prose.
 *
 * `allow_always_program` is the one option whose label is not a fixed string:
 * the backend names the grant it would store ("Always allow git status"), and
 * that grant is not carried in a separate field. It is recovered from the name
 * by stripping the known English prefix -- the same "match the backend's text"
 * approach `compactionSummary.ts` and `loopGuardNotice.ts` already use. An
 * unrecognised shape falls back to the backend's own text, which is still
 * correct, merely untranslated.
 */
export function permissionOptionLabel(opt: CoddyPermissionOption): string {
  switch (opt.optionId) {
    case "allow":
      return t("prompts.allow");
    case "allow_always":
      return t("prompts.allowAlways");
    case "reject":
      return t("prompts.reject");
    case "allow_always_program": {
      const grant = programGrantFromOptionName(opt.name);
      return grant
        ? t("prompts.allowAlwaysProgram", { program: grant })
        : opt.name;
    }
    default:
      return opt.name;
  }
}

/** The English prefix `internal/permission.Options` builds the label with. */
const PROGRAM_OPTION_PREFIX = "Always allow ";

export function programGrantFromOptionName(name: string): string {
  const raw = (name || "").trim();
  if (!raw.startsWith(PROGRAM_OPTION_PREFIX)) {
    return "";
  }
  return raw.slice(PROGRAM_OPTION_PREFIX.length).trim();
}
