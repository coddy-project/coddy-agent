import { useState, type InputHTMLAttributes } from "react";
import { IconEye } from "./icons";
import { useT } from "../i18n/I18nProvider";

export function SecretInput(
  props: InputHTMLAttributes<HTMLInputElement> & {
    children?: React.ReactNode;
  },
) {
  const [visible, setVisible] = useState(false);
  const { t } = useT();
  const { children, ...inputProps } = props;
  return (
    <div className="settings-secret-input">
      <input
        {...inputProps}
        className={inputProps.className ?? "settings-input"}
        type={visible ? "text" : "password"}
      />
      <button
        type="button"
        className="settings-btn-icon"
        aria-label={t(
          visible ? "settings.auth.hidePassword" : "settings.auth.showPassword",
        )}
        aria-pressed={visible}
        onClick={() => setVisible(!visible)}
      >
        <IconEye off={visible} />
      </button>
      {props.children}
    </div>
  );
}
