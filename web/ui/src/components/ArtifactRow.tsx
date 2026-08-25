// One row per catalog entity. The domain browser and the search surface
// receive the same descriptor, so both render this row and neither formats
// the same field twice.

import { Badge } from './primitives';
import type { ArtifactDescriptor } from '../api';
import { artifactHref } from '../route';

/** Relevance renders the lexical rank. A result matched only by vector
 * similarity arrives with a zero score, which is a property of how it
 * matched rather than a weak match, so it is labelled instead of ranked. A
 * descriptor carrying no score at all, which is every domain-browser entry,
 * renders nothing. */
function Relevance({ score }: { score?: number }) {
  if (score === undefined) {
    return null;
  }
  if (score === 0) {
    return <span className="quiet label">matched by meaning</span>;
  }
  return <span className="quiet label mono">score {score.toFixed(2)}</span>;
}

export function ArtifactRow({ artifact }: { artifact: ArtifactDescriptor }) {
  return (
    <li className="artifact-row">
      <a className="mono artifact-id" href={artifactHref(artifact.id)}>
        {artifact.id}
      </a>
      <div className="artifact-meta">
        <Badge>{artifact.type}</Badge>
        {artifact.version !== undefined && artifact.version !== '' && <Badge tone="quiet">{artifact.version}</Badge>}
        {artifact.sensitivity !== undefined && artifact.sensitivity !== '' && (
          <Badge tone="quiet">{artifact.sensitivity}</Badge>
        )}
        {artifact.source === 'featured' && <Badge tone="accent">curated</Badge>}
        {artifact.source === 'signal' && <span className="quiet label">surfaced by usage</span>}
        {artifact.folded_from !== undefined && artifact.folded_from !== '' && (
          <Badge tone="quiet">from {artifact.folded_from}</Badge>
        )}
        <Relevance score={artifact.score} />
      </div>
      {artifact.description !== undefined && artifact.description !== '' && (
        <p className="artifact-description">{artifact.description}</p>
      )}
      {artifact.tags !== undefined && artifact.tags.length > 0 && (
        <ul className="tag-list">
          {artifact.tags.map((tag) => (
            <li key={tag} className="tag">
              {tag}
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}
