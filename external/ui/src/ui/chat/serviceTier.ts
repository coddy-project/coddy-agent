export type ServiceTierControl = {
  value: string;
  choices: string[];
  onChange: (value: string) => void;
};

const COOKIE = "coddy_service_tier";

export function readServiceTierCookie(): string {
  if (typeof document === "undefined") return "";
  const part = document.cookie
    .split(";")
    .find((part) => part.trim().startsWith(COOKIE + "="));
  try {
    return part ? decodeURIComponent(part.trim().slice(COOKIE.length + 1)) : "";
  } catch {
    return "";
  }
}

export function writeServiceTierCookie(value: string): void {
  const secure = window.location.protocol === "https:" ? "; Secure" : "";
  document.cookie = `${COOKIE}=${encodeURIComponent(value)}; Path=/; Max-Age=31536000; SameSite=Lax${secure}`;
}

export function pickServiceTier(
  value: string,
  choices: readonly string[],
): string {
  return choices.includes(value) ? value : "";
}

export function serviceTierLabelKey(value: string): string {
  return "composer.serviceTier." + (value || "auto");
}
