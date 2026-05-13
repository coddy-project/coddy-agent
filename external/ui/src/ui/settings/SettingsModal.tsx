import { useState, useEffect, useRef } from "react";
import type React from "react";

export interface SettingsModalProps {
  open: boolean;
  onClose: () => void;
  providersPanel: React.ReactNode;
  modelsPanel: React.ReactNode;
}

const tabButtonBaseStyle: React.CSSProperties = {
  padding: "10px 12px",
  fontSize: "14px",
  cursor: "pointer",
  border: "none",
  textAlign: "left",
  width: "100%",
  background: "transparent",
  color: "var(--muted)",
  borderRadius: "8px",
};

const tabButtonActiveStyle: React.CSSProperties = {
  ...tabButtonBaseStyle,
  background: "var(--nav)",
  color: "var(--text)",
};

export function SettingsModal({
  open,
  onClose,
  providersPanel,
  modelsPanel,
}: SettingsModalProps): React.ReactNode {
  const [activeTab, setActiveTab] = useState<"providers" | "models">(
    "providers",
  );

  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onCloseRef.current();
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [open]);

  if (!open) {
    return null;
  }

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        background: "var(--coddy-overlay-scrim-bg)",
        zIndex: 100,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) {
          onClose();
        }
      }}
    >
      <div
        className="settings-modal"
        role="dialog"
        aria-modal="true"
        aria-label="Settings"
      >
        <div
          style={{
            width: "180px",
            borderRight: "1px solid var(--coddy-glass-panel-border)",
            padding: "16px",
            display: "flex",
            flexDirection: "column",
            gap: "8px",
          }}
        >
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "providers"}
            style={
              activeTab === "providers"
                ? tabButtonActiveStyle
                : tabButtonBaseStyle
            }
            onClick={() => setActiveTab("providers")}
          >
            Providers
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "models"}
            style={
              activeTab === "models"
                ? tabButtonActiveStyle
                : tabButtonBaseStyle
            }
            onClick={() => setActiveTab("models")}
          >
            Models
          </button>
        </div>
        <div
          role="tabpanel"
          style={{
            flex: 1,
            padding: "24px",
            overflowY: "auto",
          }}
        >
          {activeTab === "providers" ? providersPanel : modelsPanel}
        </div>
      </div>
    </div>
  );
}
