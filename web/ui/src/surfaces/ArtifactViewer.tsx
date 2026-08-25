// The artifact viewer. It renders the manifest body as a document through the
// sanitized rendering path, presents the frontmatter as a property table
// whose values are text, and links to the artifacts that extend or depend on
// this one, which §13.10 requires.

import { ArtifactBody } from '../components/ArtifactBody';
import { Badge, CopyField, EmptyState, ErrorState, Loading } from '../components/primitives';
import { PropertyTable } from '../components/PropertyTable';
import { splitDocument } from '../frontmatter';
import type { LargeResourceLink, LoadArtifactResponse } from '../api';
import { dependentsOf, loadArtifact } from '../api';
import { artifactHref } from '../route';
import { useAsync, useErrorReport } from '../useAsync';

export function ArtifactViewer({ id, onError }: { id: string; onError: (err: unknown) => void }) {
  const artifact = useAsync(() => loadArtifact(id), [id]);
  useErrorReport(artifact.error, onError);

  if (artifact.loading) {
    return <Loading label="Loading the artifact." />;
  }
  if (artifact.error !== null) {
    return <ErrorState error={artifact.error} onRetry={artifact.reload} />;
  }
  const body = artifact.value;
  if (body === null) {
    return null;
  }
  return (
    <section className="surface" aria-label="Artifact viewer">
      <h1 className="mono">{body.id}</h1>
      <div className="artifact-meta">
        <Badge>{body.type}</Badge>
        <Badge tone="quiet">{body.version}</Badge>
        {body.sensitivity !== undefined && body.sensitivity !== '' && <Badge tone="quiet">{body.sensitivity}</Badge>}
        {body.layer !== undefined && body.layer !== '' && <Badge tone="quiet">layer {body.layer}</Badge>}
      </div>
      <ManifestDocument artifact={body} />
      <Relations id={body.id} />
      <Resources artifact={body} />
      <h2>Provenance</h2>
      <p className="mono quiet">{body.content_hash}</p>
    </section>
  );
}

/** ManifestDocument renders the manifest: its body as a document, its
 * frontmatter as a property table, and the authored skill file where the
 * artifact carries one. A manifest above the inline cutoff arrives as a
 * presigned URL with the inline body empty, so the viewer has nothing to
 * render until that fetch completes and carries its own loading and failure
 * states, which leave the rest of the page usable. */
function ManifestDocument({ artifact }: { artifact: LoadArtifactResponse }) {
  const link = artifact.manifest_body_url;
  const fetched = useAsync(async () => (link === undefined ? '' : fetchText(link)), [link?.presigned_url ?? '']);
  if (link === undefined) {
    return (
      <Manifest
        body={artifact.manifest_body}
        frontmatter={artifact.frontmatter}
        skillRaw={artifact.skill_raw ?? ''}
      />
    );
  }
  if (fetched.loading) {
    return <Loading label="Fetching the artifact." />;
  }
  if (fetched.error !== null) {
    return <ErrorState error={fetched.error} onRetry={fetched.reload} />;
  }
  // The presigned channel delivers the canonical manifest document rather
  // than a body, and the response cleared the field that document
  // duplicates, so the client reconstitutes both halves from it: the body
  // reaches the rendering path and the frontmatter block reaches the
  // property table. The fences reach neither, and the response's own
  // frontmatter wins where it survived, which is the skill case, where the
  // fetched document is the authored skill file rather than the manifest.
  const split = splitDocument(fetched.value ?? '');
  return (
    <Manifest
      body={split.body}
      frontmatter={artifact.frontmatter === '' ? split.frontmatter : artifact.frontmatter}
      skillRaw={artifact.skill_raw ?? ''}
    />
  );
}

function Manifest({ body, frontmatter, skillRaw }: { body: string; frontmatter: string; skillRaw: string }) {
  return (
    <>
      <ArtifactBody body={body} />
      <PropertyTable raw={frontmatter} />
      <AuthoredSource raw={skillRaw} />
    </>
  );
}

/** AuthoredSource shows the authored skill file byte for byte. A skill
 * artifact carries one and every other type carries none, and the registry
 * clears it when the manifest arrives by link, so the section is absent
 * more often than it is present and it renders nothing at all when it is
 * absent rather than standing an empty container in the layout. */
function AuthoredSource({ raw }: { raw: string }) {
  if (raw === '') {
    return null;
  }
  return (
    <section aria-label="Authored source">
      <h2>Authored source</h2>
      <CopyField label="SKILL.md" value={raw} block />
    </section>
  );
}

async function fetchText(link: LargeResourceLink): Promise<string> {
  const response = await fetch(link.presigned_url);
  if (!response.ok) {
    throw new Error(`the manifest body fetch answered ${response.status}`);
  }
  return response.text();
}

/** Relations lists the artifacts that extend or otherwise depend on this
 * one. The edges arrive on their own request, so an artifact with no edges
 * is a state of this section rather than of the page. */
function Relations({ id }: { id: string }) {
  const edges = useAsync(() => dependentsOf(id), [id]);
  return (
    <section aria-label="Relations">
      <h2>Relations</h2>
      {edges.loading && <Loading label="Loading relations." />}
      {edges.error !== null && <ErrorState error={edges.error} onRetry={edges.reload} />}
      {edges.value !== null &&
        (edges.value.length === 0 ? (
          <EmptyState>Nothing extends or depends on this artifact.</EmptyState>
        ) : (
          <ul className="relation-list">
            {edges.value.map((edge) => (
              <li key={edge.kind + edge.from}>
                <span className="label quiet">{edge.kind}</span>{' '}
                <a className="mono" href={artifactHref(edge.from)}>
                  {edge.from}
                </a>
              </li>
            ))}
          </ul>
        ))}
    </section>
  );
}

/** Resources renders the bundled files as one list. The registry splits them
 * one by one, so a single artifact can carry inline files beside fetched
 * ones, and the delivery column is what tells them apart. */
function Resources({ artifact }: { artifact: LoadArtifactResponse }) {
  const inline = Object.keys(artifact.resources ?? {});
  const fetched = Object.entries(artifact.large_resources ?? {});
  if (inline.length === 0 && fetched.length === 0) {
    return null;
  }
  return (
    <section aria-label="Resources">
      <h2>Resources</h2>
      <table className="data-table">
        <thead>
          <tr>
            <th>File</th>
            <th>Delivery</th>
            <th>Size</th>
          </tr>
        </thead>
        <tbody>
          {inline.map((name) => (
            <tr key={name}>
              <td className="mono">{name}</td>
              <td>{artifact.resources_base64 === true ? 'inline, base64' : 'inline'}</td>
              <td className="mono">{(artifact.resources ?? {})[name].length} bytes</td>
            </tr>
          ))}
          {fetched.map(([name, link]) => (
            <tr key={name}>
              <td className="mono">
                <a href={link.presigned_url}>{name}</a>
              </td>
              <td>fetched on demand</td>
              <td className="mono">{link.size} bytes</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
