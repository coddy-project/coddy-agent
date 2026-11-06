export type OnboardingStatus = {
  first_run: boolean;
  has_config: boolean;
  has_providers: boolean;
  has_models: boolean;
  has_agent_model: boolean;
  has_agent_credentials: boolean;
  missing_api_keys: string[];
};

export async function fetchOnboardingStatus(): Promise<OnboardingStatus | null> {
  try {
    const res = await fetch("/coddy/onboarding/status");
    if (!res.ok) return null;
    const body = (await res.json()) as Partial<OnboardingStatus>;
    if (typeof body.has_agent_credentials !== "boolean") return null;
    return body as OnboardingStatus;
  } catch {
    return null;
  }
}

export function shouldShowOnboarding(status: OnboardingStatus | null): boolean {
  if (!status) return false;
  return (
    status.first_run ||
    !status.has_providers ||
    !status.has_models ||
    !status.has_agent_model ||
    status.has_agent_credentials === false
  );
}
