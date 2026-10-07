import { useId } from "react";

import { useT } from "../i18n/I18nProvider";
import { FieldLabel } from "./FieldHint";
import { sharedAliasIsValid } from "./sharedModels";
import { SwitchField } from "./SwitchField";

function text(v: unknown): string {
  return v === undefined || v === null ? "" : String(v);
}

/**
 * SharedAsField edits models[].shared_as: the alias under which another Coddy
 * may use the model through this server. Empty keeps the model private, and a
 * value the server would refuse (the alias pattern) is named under the field
 * before Save does it.
 */
export function SharedAsField(props: {
  value: unknown;
  onChange: (v: string) => void;
  label: string;
  description?: string | undefined;
}) {
  const { value, onChange, label, description } = props;
  const { t } = useT();
  const errorId = useId();
  const invalid = !sharedAliasIsValid(value);
  return (
    <div className="settings-row" data-testid="shared-as-field">
      <FieldLabel label={label} description={description} />
      <input
        className="settings-input"
        type="text"
        value={text(value)}
        placeholder={t("settings.sharedAs.placeholder")}
        aria-label={label}
        aria-invalid={invalid ? true : undefined}
        aria-describedby={invalid ? errorId : undefined}
        spellCheck={false}
        autoComplete="off"
        data-testid="shared-as-input"
        onChange={(e) => onChange(e.target.value)}
      />
      {invalid ? (
        <p
          id={errorId}
          className="settings-field-desc settings-error"
          data-testid="shared-as-error"
        >
          {t("settings.sharedAs.invalid")}
        </p>
      ) : null}
    </div>
  );
}

/**
 * SharedSubscriptionAckField is models[].shared_subscription_ack for a shared
 * model whose provider runs on a subscription login: the operator's written
 * acceptance that the login's quota goes to every holder of a shared-model
 * token and that the vendor's terms may forbid it. The server refuses the
 * configuration without it, so the field is marked required and the warning
 * the refusal carries stands beside it, in the error tone until it is
 * accepted. The caller shows the field only while it matters.
 */
export function SharedSubscriptionAckField(props: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  /** The providers[] row the model runs on, named in the warning. */
  providerName: string;
  providerType: string;
}) {
  const { checked, onChange, label, providerName, providerType } = props;
  const { t } = useT();
  const warningId = useId();
  return (
    <div className="shared-ack" data-testid="shared-ack">
      <SwitchField
        checked={checked}
        onChange={onChange}
        label={
          <>
            {label}{" "}
            <span className="settings-required-mark">
              {t("settings.sharedAck.required")}
            </span>
          </>
        }
        ariaRequired
        ariaInvalid={!checked}
        ariaDescribedBy={warningId}
        dataTestId="shared-subscription-ack"
      />
      <p
        id={warningId}
        className={`settings-field-desc${checked ? "" : " settings-error"}`}
        data-testid="shared-ack-warning"
      >
        {t("settings.sharedAck.warning", {
          provider: providerName,
          type: providerType,
        })}
      </p>
    </div>
  );
}

/**
 * CoddyAPIBaseField is providers[].api_base of a provider of type coddy: the
 * address of the remote coddy serve, or of a relay mount. The ordinary field
 * would show the OpenAI address as its example.
 */
export function CoddyAPIBaseField(props: {
  value: unknown;
  onChange: (v: string) => void;
  label: string;
  description?: string | undefined;
}) {
  const { value, onChange, label, description } = props;
  const { t } = useT();
  return (
    <div className="settings-row">
      <FieldLabel label={label} description={description} />
      <input
        className="settings-input"
        type="text"
        inputMode="url"
        value={text(value)}
        placeholder={t("settings.coddyApiBase.placeholder")}
        aria-label={label}
        spellCheck={false}
        autoComplete="off"
        data-testid="coddy-api-base"
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  );
}
