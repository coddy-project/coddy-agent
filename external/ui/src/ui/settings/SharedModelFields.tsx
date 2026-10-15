import { useId } from "react";

import { useT } from "../i18n/I18nProvider";
import { FieldLabel } from "./FieldHint";
import { sharedAliasIsValid } from "./sharedModels";
import { SwitchField } from "./SwitchField";

function text(value: unknown): string {
  return value === undefined || value === null ? "" : String(value);
}

export function SharedAsField(props: {
  value: unknown;
  onChange: (value: string) => void;
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
        aria-invalid={invalid || undefined}
        aria-describedby={invalid ? errorId : undefined}
        spellCheck={false}
        autoComplete="off"
        data-testid="shared-as-input"
        onChange={(event) => onChange(event.target.value)}
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

export function SharedSubscriptionAckField(props: {
  checked: boolean;
  onChange: (value: boolean) => void;
  label: string;
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

export function CoddyAPIBaseField(props: {
  value: unknown;
  onChange: (value: string) => void;
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
        onChange={(event) => onChange(event.target.value)}
      />
    </div>
  );
}
