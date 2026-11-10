import { useState, type InputHTMLAttributes } from "react";
import { IconEye } from "./icons";
import { useT } from "../i18n/I18nProvider";

export function SecretInput(props: InputHTMLAttributes<HTMLInputElement>) {
  const [visible, setVisible] = useState(false);
  const { t } = useT();
  return (
    <div style={{ display: "flex", minWidth: 0 }}>
      <input
        {...props}
        className={props.className ?? "settings-input"}
        type={visible ? "text" : "password"}
      />
      <button
        type="button"
        className="settings-btn-icon"
        aria-label={t(
          visible ? "settings.auth.hidePassword" : "settings.auth.showPassword",
        )}
        onClick={() => setVisible(!visible)}
      >
        <IconEye off={visible} />
      </button>
    </div>
  );
}
