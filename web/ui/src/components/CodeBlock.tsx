// The bordered file view the artifact viewer's code panes share: a header
// naming the block and its extent, a numbered gutter, and the text beside it.
// Both panes on the surface are a run of authored lines a reader quotes from,
// so they carry the same chrome and a line the caller marks is tinted in both
// columns (§13.10).

/** CodeBlock lays out one pane. `name` and `extra` fill the header, `lines`
 * is the text already split, and `offending` is the one-based line the caller
 * wants marked, or 0 for none. A caller that needs the pane reachable from
 * the keyboard names it in `label`: the text column scrolls sideways, and
 * without a name and a tab stop a keyboard-only reader cannot reach a value
 * that runs past the pane's right edge. */
export function CodeBlock({
  name,
  extra,
  lines,
  offending = 0,
  label,
  testID,
}: {
  name: string;
  extra?: string;
  lines: readonly string[];
  offending?: number;
  label?: string;
  testID?: string;
}) {
  return (
    <div className="source-block">
      <div className="source-head mono">
        <span>{name}</span>
        <span className="quiet">
          {lines.length} {lines.length === 1 ? 'line' : 'lines'}
          {extra !== undefined && ` · ${extra}`}
        </span>
      </div>
      <div className="source-lines">
        {/* The gutter is decorative for a reader who is listening rather than
            looking: it repeats no content, and a screen reader that read it
            would interleave numbers with the file's own text. */}
        <div className="source-gutter mono" aria-hidden="true">
          {lines.map((line, index) => (
            <div
              key={`${String(index)}:${line}`}
              className={index + 1 === offending ? 'source-gutter-offending' : undefined}
            >
              {index + 1}
            </div>
          ))}
        </div>
        <pre
          className="mono source-code"
          data-testid={testID}
          tabIndex={label === undefined ? undefined : 0}
          role={label === undefined ? undefined : 'region'}
          aria-label={label}
        >
          {lines.map((line, index) => (
            <span
              key={`${String(index)}:${line}`}
              className={index + 1 === offending ? 'raw-line raw-line-offending' : 'raw-line'}
              data-testid={index + 1 === offending ? 'offending-line' : undefined}
            >
              {line}
              {index + 1 < lines.length && '\n'}
            </span>
          ))}
        </pre>
      </div>
    </div>
  );
}

/** codeLines splits a block into the lines an editor would number. A file
 * ends with a newline, and splitting on it yields a trailing empty element
 * that is not a line, so one trailing newline is dropped. */
export function codeLines(value: string): string[] {
  return value.replace(/\n$/, '').split('\n');
}
