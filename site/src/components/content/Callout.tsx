import type { ReactNode } from "react";

const LABELS: Record<string, string> = {
  note: "Note",
  tip: "Tip",
  important: "Important",
  warning: "Warning",
  caution: "Caution",
};

/**
 * Renders a GitHub alert blockquote. The type comes from the `[!NOTE]` marker
 * the transform stripped, so the same markdown reads as an alert on GitHub and
 * as a callout here.
 */
export function Callout(props: { type?: string; children?: ReactNode }) {
  const type = props.type ?? "note";
  const label = LABELS[type] ?? LABELS["note"]!;
  return (
    <div className={`callout callout-${type}`} role="note">
      <p className="callout-label">{label}</p>
      <div className="callout-body">{props.children}</div>
    </div>
  );
}
