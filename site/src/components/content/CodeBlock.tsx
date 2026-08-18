import type { ReactNode } from "react";

const DISPLAY: Record<string, string> = {
  bash: "shell",
  javascript: "js",
  typescript: "ts",
  markdown: "md",
  powershell: "pwsh",
  python: "py",
  yaml: "yaml",
  json: "json",
  go: "go",
  text: "text",
};

/**
 * Wraps a highlighted fence with its language label and a copy control.
 *
 * The markup Shiki produced is passed straight through as children: it already
 * carries both themes as CSS variables, so switching the theme repaints without
 * re-highlighting and no highlighter reaches the browser.
 *
 * The copy button carries no handler here. The client runtime binds it, so a
 * reader without JavaScript sees the code and loses only the shortcut.
 */
export function CodeBlock(props: {
  children?: ReactNode;
  "data-language"?: string;
  "data-highlighted"?: string;
  className?: string;
  style?: Record<string, string>;
}) {
  const language = props["data-language"] ?? "text";
  const label = DISPLAY[language] ?? language;

  return (
    <div className="code-block" data-language={language}>
      <div className="code-block-bar">
        <span className="code-block-language">{label}</span>
        <button type="button" className="code-block-copy" data-copy aria-label="Copy code">
          copy
        </button>
      </div>
      <pre
        className={props.className}
        style={props.style}
        data-language={language}
        data-highlighted={props["data-highlighted"]}
        tabIndex={0}
      >
        {props.children}
      </pre>
    </div>
  );
}
