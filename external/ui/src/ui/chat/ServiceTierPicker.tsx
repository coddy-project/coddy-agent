export type ServiceTierControl = {
  value: string;
  choices: string[];
  onChange: (tier: string) => void;
};

export function serviceTierLabel(
  tier: string,
  t: (key: string) => string,
): string {
  const keys: Record<string, string> = {
    "": "composer.serviceTierDefault",
    default: "composer.serviceTierStandard",
    priority: "composer.serviceTierFast",
    fast: "composer.serviceTierFast",
    ultrafast: "composer.serviceTierUltra",
  };
  return keys[tier] ? t(keys[tier]) : tier;
}
