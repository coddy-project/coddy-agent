import type { CoddyPermissionOption } from "./permissionTypes";

export const DEFAULT_PERMISSION_OPTIONS: CoddyPermissionOption[] = [
  { optionId: "allow", name: "Allow", kind: "allow_once" },
  { optionId: "allow_always", name: "Allow always", kind: "allow_always" },
  { optionId: "reject", name: "Reject", kind: "reject_once" },
];
