// Registering a layer. Visibility is a set of independent grants that
// combine as a union, so the form offers them as combinable checkboxes and
// says once that visibility is fixed at registration. A git source returns a
// webhook URL and an HMAC secret, and that response and a secret rotation are
// the only places the secret is returned, so the reveal states that it is
// shown once and stays until the reader acknowledges it.

import type { FormEvent } from 'react';
import { useState } from 'react';

import { Banner, CopyField, ErrorState } from '../components/primitives';
import type { LayerRegisterResult, LayerRegistration } from '../api';
import { registerLayer } from '../api';

export function RegisterLayerForm({ onRegistered, readOnly }: { onRegistered: () => void; readOnly: boolean }) {
  const [id, setID] = useState('');
  const [sourceType, setSourceType] = useState('git');
  const [repo, setRepo] = useState('');
  const [ref, setRef] = useState('');
  const [localPath, setLocalPath] = useState('');
  const [isPublic, setPublic] = useState(false);
  const [organization, setOrganization] = useState(false);
  const [groupScoped, setGroupScoped] = useState(false);
  const [groups, setGroups] = useState('');
  const [userScoped, setUserScoped] = useState(false);
  const [users, setUsers] = useState('');
  const [result, setResult] = useState<LayerRegisterResult | null>(null);
  const [refusal, setRefusal] = useState<unknown>(null);

  // An axis the reader turned on with no member named would register a
  // grant that admits nobody, so the form holds the write until each
  // selected axis carries at least one member.
  const groupMembers = members(groups);
  const userMembers = members(users);
  const incomplete = (groupScoped && groupMembers.length === 0) || (userScoped && userMembers.length === 0);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const body: LayerRegistration = {
      id,
      source_type: sourceType,
      repo: sourceType === 'git' ? repo : undefined,
      ref: sourceType === 'git' ? ref : undefined,
      local_path: sourceType === 'local' ? localPath : undefined,
      public: isPublic,
      organization,
      groups: groupScoped ? groupMembers : undefined,
      users: userScoped ? userMembers : undefined,
    };
    registerLayer(body).then(
      (next) => {
        setRefusal(null);
        setResult(next);
        onRegistered();
      },
      (err: unknown) => {
        setResult(null);
        setRefusal(err);
      },
    );
  };

  if (result !== null) {
    return (
      <SecretReveal
        result={result}
        onDone={() => {
          setResult(null);
        }}
      />
    );
  }

  return (
    <form className="register-form" aria-label="Register a layer" onSubmit={submit}>
      <label className="field">
        <span className="label">Layer ID</span>
        <input
          type="text"
          value={id}
          onChange={(event) => {
            setID(event.target.value);
          }}
        />
      </label>
      <label className="field">
        <span className="label">Source</span>
        <select
          value={sourceType}
          onChange={(event) => {
            setSourceType(event.target.value);
          }}
        >
          <option value="git">Git repository</option>
          <option value="local">Local folder</option>
        </select>
      </label>
      {sourceType === 'git' ? (
        <>
          <label className="field">
            <span className="label">Repository</span>
            <input
              type="text"
              value={repo}
              onChange={(event) => {
                setRepo(event.target.value);
              }}
            />
          </label>
          <label className="field">
            <span className="label">Ref</span>
            <input
              type="text"
              value={ref}
              onChange={(event) => {
                setRef(event.target.value);
              }}
            />
          </label>
        </>
      ) : (
        <label className="field">
          <span className="label">Local path</span>
          <input
            type="text"
            value={localPath}
            onChange={(event) => {
              setLocalPath(event.target.value);
            }}
          />
        </label>
      )}
      <fieldset className="field">
        <legend className="label">Visibility</legend>
        <label>
          <input
            type="checkbox"
            checked={isPublic}
            onChange={(event) => {
              setPublic(event.target.checked);
            }}
          />
          Public
        </label>
        <label>
          <input
            type="checkbox"
            checked={organization}
            onChange={(event) => {
              setOrganization(event.target.checked);
            }}
          />
          Organization
        </label>
        <label>
          <input
            type="checkbox"
            checked={groupScoped}
            onChange={(event) => {
              setGroupScoped(event.target.checked);
            }}
          />
          Groups
        </label>
        {groupScoped && (
          <label className="field">
            <span className="label">Group names, separated by commas</span>
            <input
              type="text"
              value={groups}
              onChange={(event) => {
                setGroups(event.target.value);
              }}
            />
          </label>
        )}
        <label>
          <input
            type="checkbox"
            checked={userScoped}
            onChange={(event) => {
              setUserScoped(event.target.checked);
            }}
          />
          Specific users
        </label>
        {userScoped && (
          <label className="field">
            <span className="label">User identifiers, separated by commas</span>
            <input
              type="text"
              value={users}
              onChange={(event) => {
                setUsers(event.target.value);
              }}
            />
          </label>
        )}
        {/* §4.6 combines the axes as a union, so a layer carrying more than
            one grant is visible to a caller who matches any of them. */}
        <p className="quiet">A caller who matches any selected grant sees this layer.</p>
        <p className="quiet">Visibility is fixed at registration.</p>
      </fieldset>
      <button type="submit" disabled={readOnly || incomplete}>
        Register
      </button>
      {/* The registry refuses a registration past the per-user cap with an
          error carrying the limit and the caller's current count, and this is
          where the user creates the layer, so the refusal is presented here
          rather than as a failure of the page. */}
      {refusal !== null && <ErrorState error={refusal} />}
    </form>
  );
}

/** members splits a comma-separated member list into the identifiers the
 * registration carries, dropping the empty runs a trailing separator or a
 * stray space leaves behind. */
function members(raw: string): string[] {
  return raw
    .split(',')
    .map((member) => member.trim())
    .filter((member) => member !== '');
}

function SecretReveal({ result, onDone }: { result: LayerRegisterResult; onDone: () => void }) {
  const [acknowledged, setAcknowledged] = useState(false);
  if (result.webhook_secret === undefined || result.webhook_secret === '') {
    // A local-path source returns neither a webhook URL nor a secret, so the
    // whole reveal is conditional.
    return <Banner tone="accent">Layer {result.layer.ID} is registered.</Banner>;
  }
  return (
    <div className="secret-reveal" aria-label="Webhook secret">
      <p className="label">Shown once</p>
      <p>
        The webhook URL is permanent. The secret is returned here and on a rotation, and the registry stores only a
        hash of it.
      </p>
      {/* Both values are here to be taken away, so each carries its own
          copy control. The secret is never served again, so a reader who
          leaves without it has to rotate the secret to get another. */}
      <CopyField label="Webhook URL" value={result.webhook_url ?? ''} />
      <CopyField label="Webhook secret" value={result.webhook_secret} />
      <label>
        <input
          type="checkbox"
          checked={acknowledged}
          onChange={(event) => {
            setAcknowledged(event.target.checked);
          }}
        />
        I have stored the secret.
      </label>
      <button type="button" disabled={!acknowledged} onClick={onDone}>
        Done
      </button>
    </div>
  );
}
