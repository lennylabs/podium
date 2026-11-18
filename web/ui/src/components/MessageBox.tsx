// The registry's own words about an artifact's overlap and description
// length. The messages are prose the registry writes, and the surface states
// them verbatim rather than rebuilding them, because the structured peers
// already carry everything a rebuilt sentence would say.

import type { OverlapDiagnostic } from '../api';
import { semanticCode } from '../similarity';

/** MessageBox states an artifact's overlap diagnostics in one box, one line
 * each, in the registry's words. The dot follows the code: the accent for a
 * semantic similarity and grey for a word overlap, which are the colours the
 * two scores carry in the table under it. A line too long for the box is cut
 * with an ellipsis and carries the whole message as its title.
 *
 * Spec: §13.10 */
export function MessageBox({ diagnostics }: { diagnostics: OverlapDiagnostic[] }) {
  if (diagnostics.length === 0) {
    return null;
  }
  return (
    <ul className="message-box" aria-label="Overlap diagnostics">
      {diagnostics.map((d) => (
        <li key={`${d.code} ${d.message}`} className="message-line">
          <span
            className={d.code === semanticCode ? 'message-dot message-dot-similarity' : 'message-dot message-dot-word'}
            aria-hidden="true"
          />
          <span className="message-text" title={d.message}>
            {d.message}
          </span>
        </li>
      ))}
    </ul>
  );
}

/** ThinNotice states the description-length diagnostic: a short heading
 * naming the problem and the registry's message under it. It is a warning
 * about the artifact's own description rather than an error, so it takes the
 * accent dot on a plain card.
 *
 * Spec: §13.10 */
export function ThinNotice({ message }: { message: string }) {
  return (
    <div className="thin-notice" role="note" data-testid="thin-notice">
      <span className="message-dot message-dot-similarity thin-notice-dot" aria-hidden="true" />
      <div>
        <p className="thin-notice-title">Too short to tell apart</p>
        <p className="thin-notice-message">{message}</p>
      </div>
    </div>
  );
}

/** OverlapEmpty is the dashed card a similarity listing stands on when the
 * report holds nothing for it. It states only what the read returned, so a
 * hidden artifact and one that overlaps nothing read the same. */
export function OverlapEmpty({ children, testID }: { children: string; testID?: string }) {
  return (
    <div className="overlap-empty" data-testid={testID}>
      <p className="overlap-empty-title">{children}</p>
    </div>
  );
}
