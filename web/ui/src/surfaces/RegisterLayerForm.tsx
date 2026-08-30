// Registering a layer. The registration is reviewed before it is sent and
// the panel underneath keeps its position while it is, so the form is a
// dialog over a scrim rather than a section pushed into the panel.
// The panel serves both halves of the §13.10 role split,
// so the form carries the layer class as a control. A user registering their
// own layer creates a user-defined one, which is the class §7.3.1 caps per
// user and authorizes its owner on, and the registry fixes such a layer's
// visibility to the registrant and discards any visibility the request
// carries, so the axes are offered on the admin-defined class alone.
// Visibility there is a set of independent grants that combine as a union, so
// the form offers them as combinable checkboxes. A git source returns a
// webhook URL and an HMAC secret, and that response and a secret rotation are
// the only places the secret is returned, so the reveal states that it is
// shown once and stays until the reader acknowledges it.

import type { FormEvent, KeyboardEvent, ReactNode } from 'react';
import { useEffect, useId, useRef, useState } from 'react';

import { SecretReveal, useSecretAcknowledgement } from './SecretReveal';
import { fragment, matchGroups, members, replaceFragment, without } from './members';
import { ErrorState, Modal } from '../components/primitives';
import type { LayerRegistration, LayerSecretResult } from '../api';
import { ApiError, registerLayer } from '../api';

export function RegisterLayerForm({
  subject,
  knownGroups,
  knownIDs,
  onRegistered,
  onClose,
  readOnly,
}: {
  subject: string;
  /** knownGroups are the group names already granted on the layers the caller
   * can see. They back the group axis's typeahead, which is the only check the
   * form can offer on a name before it is sent. */
  knownGroups: string[];
  /** knownIDs are the IDs of the layers the panel already lists. §4.6 keys a
   * layer on its ID and the registration is an upsert on that key, so a
   * registration reusing one rewrites the stored layer, reassigning its place
   * in the order and clearing its ingest state. The list covers the layers the
   * caller can see, which is what the form can check a posted ID against
   * before it sends one. */
  knownIDs: readonly string[];
  onRegistered: () => void;
  onClose: () => void;
  readOnly: boolean;
}) {
  const [id, setID] = useState('');
  const [sourceType, setSourceType] = useState('git');
  const [repo, setRepo] = useState('');
  const [ref, setRef] = useState('');
  // Only the git source reads the root: it names the subtree the layer's
  // artifacts live under, and a repository holding them below its top level
  // cannot be registered without it.
  const [root, setRoot] = useState('');
  const [localPath, setLocalPath] = useState('');
  // The class defaults to the one the caller can register. A user-defined
  // layer's owner is derived from the caller's own subject and the registry
  // refuses the registration where none resolves, so a caller who holds a
  // subject opens on their own layer and a caller who holds none opens on the
  // tenant's. Either way the class stays a control, because the posture read
  // reports no role and the panel predicts no outcome.
  const [userDefined, setUserDefined] = useState(subject !== '');
  const [isPublic, setPublic] = useState(false);
  const [organization, setOrganization] = useState(false);
  const [groupScoped, setGroupScoped] = useState(false);
  const [groups, setGroups] = useState('');
  const [userScoped, setUserScoped] = useState(false);
  const [users, setUsers] = useState('');
  const [result, setResult] = useState<LayerSecretResult | null>(null);
  const secret = useSecretAcknowledgement();
  const [refusal, setRefusal] = useState<unknown>(null);
  // The dialog opens with every required field empty, so a hold stands from
  // the first paint. Stating it then would open the form on a sentence in the
  // refusal colour, which reads as an error the reader has already caused
  // rather than as the requirement it is. The footer therefore keeps its
  // standing note until the reader has begun to fill the form in, on the same
  // terms as the per-field invalid mark below.
  const [engaged, setEngaged] = useState(false);
  // §4.6 keys a layer on its ID and the registration is an upsert on that
  // key, so a second registration sent while the first is still open rewrites
  // the layer the first one created: it reassigns the layer's place in the
  // order and issues a fresh webhook secret, which replaces the one the reveal
  // is presenting as shown once. The form therefore holds the write while one
  // is open, on the same terms as the row's Reingest control.
  const [pending, setPending] = useState(false);
  const refusalRef = useRef<HTMLDivElement | null>(null);

  // The form is taller than the dialog and the body scrolls, so a refusal
  // appended under the last field lands below the fold and the submit reads
  // as a control that did nothing. It is drawn at the head of the body, and
  // on arrival it is scrolled to and takes focus, which puts it where the
  // reader is looking wherever the body was scrolled to and announces it to
  // a reader who is not looking at the dialog at all.
  useEffect(() => {
    const banner = refusalRef.current;
    if (refusal === null || banner === null) {
      return;
    }
    banner.scrollIntoView({ block: 'nearest' });
    banner.focus();
  }, [refusal]);

  // A refusal describes the request that was sent, so the first edit after it
  // invalidates it and the banner is dropped rather than standing until the
  // next submit, asserting a complaint about a field the reader has since
  // corrected. The form's own change event covers every native control in it.
  // The controls drawn as buttons rather than as inputs, the source segments
  // and the token rows a member field draws, emit no change event, so they
  // clear the refusal through the setter they are given.
  const edited =
    <T,>(set: (next: T) => void) =>
    (next: T) => {
      setRefusal(null);
      setEngaged(true);
      set(next);
    };

  // An axis the reader turned on with no member named would register a
  // grant that admits nobody, so the form holds the write until each
  // selected axis carries at least one member.
  const groupMembers = members(groups);
  const userMembers = members(users);
  // §4.6: the git source reads its tree from a repository at a ref, and
  // neither has a default. The registration itself is accepted with either
  // blank, so a layer registered without one issues its webhook secret, takes
  // a place in the order, and is then refused on every ingest with
  // "git source requires repo" or "git source requires ref". The form holds
  // the write until both are named rather than handing the reader a layer
  // that can never serve an artifact.
  // §4.6: a local source reads its tree from the named directory and has no
  // default either, so the same hold applies on that arm. A registration with
  // the path blank is accepted and then refused on every ingest with
  // "local source requires path".
  // A held submit names what is holding it. The reader is otherwise left
  // clicking a disabled control that reports no reason, and the field the hold
  // is on scrolls out of view once the body is scrolled to the submit row.
  const hold = registrationHold({
    id,
    knownIDs,
    sourceType,
    repo,
    ref,
    localPath,
    userDefined,
    groupScoped,
    groupMembers,
    userScoped,
    userMembers,
  });
  const incomplete = hold !== null;
  // What the footer says out loud. The submit is held from the first paint,
  // and the sentence naming the hold is stated once the reader has begun.
  const stated = engaged ? hold : null;
  const holdID = useId();
  // A hold is stated in the footer, and a reader who moves into the field it
  // is on never reaches that line. The field the hold names therefore points
  // at the same sentence and reports itself invalid, so the refusal arrives
  // where it applies rather than only beside the submit.
  const heldOn = (field: HoldField) => (stated !== null && stated.field === field ? holdID : undefined);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    // The submit control disables itself while a registration is open, and
    // the guard stands here as well because a form submits on Enter from any
    // field in it, which the disabled control does not intercept.
    if (pending) {
      return;
    }
    const body: LayerRegistration = {
      id,
      source_type: sourceType,
      repo: sourceType === 'git' ? repo : undefined,
      ref: sourceType === 'git' ? ref : undefined,
      root: sourceType === 'git' ? root : undefined,
      local_path: sourceType === 'local' ? localPath : undefined,
      user_defined: userDefined,
      // The registry derives a user-defined layer's visibility from the
      // registrant and discards what the request carries, so the axes are
      // sent on the admin-defined class alone.
      public: userDefined ? undefined : isPublic,
      organization: userDefined ? undefined : organization,
      groups: !userDefined && groupScoped ? groupMembers : undefined,
      users: !userDefined && userScoped ? userMembers : undefined,
    };
    setPending(true);
    registerLayer(body).then(
      (next) => {
        setPending(false);
        setRefusal(null);
        setResult(next);
        onRegistered();
      },
      (err: unknown) => {
        setPending(false);
        setResult(null);
        setRefusal(err);
      },
    );
  };

  if (result !== null) {
    // The secret is returned on this response and nowhere else, so until the
    // reader acknowledges it the dialog closes only through the reveal's own
    // acknowledgement. Escape, the scrim, and the close control would
    // discard a credential the reader can then recover only by rotating it.
    // The acknowledgement retires that hold and the dialog dismisses again.
    return (
      <Modal title="Layer registered" onClose={onClose} dismissible={secret.dismissible(result)}>
        <SecretReveal
          result={result}
          outcome={`Layer ${result.layer.ID} is registered.`}
          acknowledged={secret.acknowledged}
          onAcknowledge={secret.setAcknowledged}
          onDone={onClose}
        >
          {/* §7.3.1: registration runs no ingest, and a git source stays at
              its initial commit until a webhook delivery or the first manual
              reingest. The row the registration adds therefore reads "never"
              and serves none of the layer's artifacts, so the outcome names
              the ingest as the next thing to run and names the control that
              runs it. */}
          <p className="note" data-testid="register-ingest-note">
            Registering does not ingest. The layer serves no artifacts until its first ingest, which the Reingest
            control on its row runs.
          </p>
        </SecretReveal>
      </Modal>
    );
  }

  return (
    <Modal
      title="Register a layer"
      description="A layer points at a source Podium ingests. Its place in the order decides who wins when two layers carry the same artifact ID."
      onClose={onClose}
    >
      <form
        className="register-form modal-form"
        data-testid="register-form"
        onSubmit={submit}
        onChange={() => {
          setRefusal(null);
          setEngaged(true);
        }}
        // Leaving a field is beginning too: the reader who tabs out of the
        // empty ID has been in the form, and the field marks itself invalid at
        // that point, so the sentence the field points at states the hold
        // rather than the standing note.
        onBlur={() => {
          setEngaged(true);
        }}
      >
        <div className="modal-body">
          {refusal !== null && (
            <div ref={refusalRef} tabIndex={-1} data-testid="register-refusal">
              <RegistrationRefusal refusal={refusal} />
            </div>
          )}
          <RequiredField
            label="Layer ID"
            value={id}
            testID="register-id"
            held={heldOn('id')}
            onChange={(next) => {
              setID(next);
            }}
          />
          <label className="field">
            <span className="label">Layer class</span>
            <select
              value={userDefined ? 'user' : 'admin'}
              onChange={(event) => {
                setUserDefined(event.target.value === 'user');
              }}
            >
              <option value="user">Your own layer</option>
              <option value="admin">A layer for the whole tenant</option>
            </select>
          </label>
          {userDefined && (
            <p className="quiet field-note">
              A layer of your own is visible to you alone, and it counts against the layer limit an administrator sets.
            </p>
          )}
          <SourceChoice value={sourceType} onChange={edited(setSourceType)} />
          {sourceType === 'git' ? (
            <>
              <RequiredField
                label="Repository"
                value={repo}
                testID="register-repo"
                held={heldOn('repo')}
                onChange={(next) => {
                  setRepo(next);
                }}
              />
              {/* The ref and the root both qualify the repository above them
                  and each holds a short value, so they share one row. */}
              <div className="field-pair">
                <RequiredField
                  label="Ref"
                  value={ref}
                  testID="register-ref"
                  held={heldOn('ref')}
                  onChange={(next) => {
                    setRef(next);
                  }}
                />
                <label className="field">
                  <span className="label">Root</span>
                  <input
                    type="text"
                    value={root}
                    onChange={(event) => {
                      setRoot(event.target.value);
                    }}
                  />
                </label>
              </div>
              <p className="quiet field-note" data-testid="register-git-note">
                Name the branch, tag, or commit to ingest as the ref; a git layer has no default and cannot ingest
                without one. Leave the root empty to ingest the whole repository, or name the subdirectory the
                artifacts live under.
              </p>
            </>
          ) : (
            <>
              <RequiredField
                label="Local path"
                value={localPath}
                testID="register-local-path"
                held={heldOn('local-path')}
                onChange={(next) => {
                  setLocalPath(next);
                }}
              />
              <p className="quiet field-note" data-testid="register-local-note">
                Name the directory the registry reads the artifacts from; a local layer has no default and cannot
                ingest without one.
              </p>
            </>
          )}
          {!userDefined && (
            <fieldset className="field visibility">
              <legend className="label">Visibility</legend>
              <p className="quiet visibility-lead">Grants combine, and anyone matching any of them sees the layer.</p>
              <div className="visibility-pair">
                <VisibilityAxis
                  name="Public"
                  description="Anyone, signed in or not."
                  checked={isPublic}
                  onChange={setPublic}
                />
                <VisibilityAxis
                  name="Organization"
                  description="Everyone in this tenant."
                  checked={organization}
                  onChange={setOrganization}
                />
              </div>
              <VisibilityAxis
                name="Groups"
                description="Members of the OIDC groups you name."
                checked={groupScoped}
                onChange={setGroupScoped}
              >
                <TokenInput
                  label="Group names, separated by commas"
                  value={groups}
                  onChange={edited(setGroups)}
                  held={heldOn('groups')}
                  tokens={groupMembers}
                  known={knownGroups}
                />
              </VisibilityAxis>
              <VisibilityAxis
                name="Specific users"
                description="Named individuals, by email."
                checked={userScoped}
                onChange={setUserScoped}
              >
                <TokenInput
                  label="User identifiers, separated by commas"
                  value={users}
                  onChange={edited(setUsers)}
                  held={heldOn('users')}
                  tokens={userMembers}
                />
              </VisibilityAxis>
              {/* §4.6 combines the axes as a union, so the consequence is
                  stated once over the whole selection rather than per axis. */}
              <p className="consequence" data-testid="visibility-consequence">
                <span className="note-glyph" aria-hidden="true">
                  ◉
                </span>
                <span className="consequence-text">
                  {consequence(isPublic, organization, groupMembers, userMembers)}
                </span>
              </p>
            </fieldset>
          )}
          {/* §4.6 fixes a user-defined layer's visibility at registration and
              the registry discards a later patch of it, so the note is stated
              on that class alone. An admin-defined layer's visibility is what
              the update endpoint patches, and the panel's Edit control is
              where that happens. */}
          <p className="note" data-testid="visibility-note">
            <span className="note-glyph" aria-hidden="true">
              ⓘ
            </span>
            <span className="note-text">
              {userDefined ? 'Visibility is fixed at registration.' : 'Visibility can be changed later from Edit.'}
            </span>
          </p>
        </div>
        <div className="modal-foot">
          {/* The registry appends a new layer at the end of the order, and
              the panel's last row is the one that wins, so the footer states
              where this registration lands rather than a fixed number. */}
          <span
            className={stated === null ? 'quiet modal-foot-note' : 'modal-foot-note modal-foot-hold'}
            id={holdID}
            data-testid="register-foot-note"
          >
            {stated?.message ?? 'Registers at the end of the order, where the last row wins.'}
          </span>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          {/* The control registers the layer and nothing else. §7.3.1 leaves
              the ingest to a webhook delivery or a manual reingest, so a
              label promising one describes a run that never happens. The
              panel's own opener carries the layer noun, so the submit inside
              the dialog the opener raised states the verb alone. */}
          <button
            type="submit"
            className="button primary"
            disabled={readOnly || incomplete || pending}
            aria-busy={pending || undefined}
            aria-describedby={stated === null ? undefined : holdID}
          >
            Register
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** HoldField identifies the field a registration hold stands on, so the field
 * itself can be marked invalid and pointed at the sentence stating the hold. */
type HoldField = 'id' | 'repo' | 'ref' | 'local-path' | 'groups' | 'users';

/** registrationHold names the field the submit is held on, or null when the
 * form is ready to send. The submit is disabled while a hold stands, and a
 * disabled control that reports no reason leaves the reader clicking a control
 * that does nothing, so every arm of the hold has a sentence naming the field
 * it is on. */
function registrationHold({
  id,
  knownIDs,
  sourceType,
  repo,
  ref,
  localPath,
  userDefined,
  groupScoped,
  groupMembers,
  userScoped,
  userMembers,
}: {
  id: string;
  knownIDs: readonly string[];
  sourceType: string;
  repo: string;
  ref: string;
  localPath: string;
  userDefined: boolean;
  groupScoped: boolean;
  groupMembers: string[];
  userScoped: boolean;
  userMembers: string[];
}): { field: HoldField; message: string } | null {
  // §4.6: a layer is addressed by its ID and every source type carries one.
  // The registry refuses a registration without it with
  // "id and source_type are required", a message naming a field the form
  // does not draw, so the form holds the write until the ID is named rather
  // than sending a request it knows will be refused.
  if (id.trim() === '') {
    return { field: 'id', message: 'Name the layer ID before registering.' };
  }
  // §4.6 keys a layer on its ID, and the registration writes that key rather
  // than refusing a reused one: the stored layer is rewritten, its place in
  // the order is reassigned to the end, and its last ingest is cleared, so a
  // live layer is reset by a registration that reports plain success. The
  // panel already lists the IDs, so the form holds the write on a reused one
  // and names the layer it would overwrite.
  if (knownIDs.some((known) => known === id.trim())) {
    return {
      field: 'id',
      message: `Layer ${id.trim()} is already registered. Registering it again would reset its place in the order and its last ingest, so name an unused ID.`,
    };
  }
  // §4.6: the git source reads its tree from the named repository and has no
  // default. A registration with the repository blank is accepted and then
  // refused on every ingest with "git source requires repo", the same failure
  // the blank ref produces, so the same hold applies to both fields.
  if (sourceType === 'git' && repo.trim() === '') {
    return { field: 'repo', message: 'Name the repository before registering.' };
  }
  if (sourceType === 'git' && ref.trim() === '') {
    return { field: 'ref', message: 'Name the ref before registering.' };
  }
  if (sourceType === 'local' && localPath.trim() === '') {
    return { field: 'local-path', message: 'Name the local path before registering.' };
  }
  if (!userDefined && groupScoped && groupMembers.length === 0) {
    return { field: 'groups', message: 'Name at least one group before registering.' };
  }
  if (!userDefined && userScoped && userMembers.length === 0) {
    return { field: 'users', message: 'Name at least one user before registering.' };
  }
  return null;
}

/** useHeldInvalid decides whether the field a hold stands on marks itself
 * invalid, and carries the blur handler that arms the mark. The dialog opens
 * with every required field empty, so a field that reported itself invalid
 * from the hold alone would announce a refusal on a form the reader has not
 * begun to fill in, on the very control that takes focus. The visible
 * requirement marker, `aria-required`, and the footer sentence carry the
 * requirement until the reader has been in the field and left it empty. */
function useHeldInvalid(held: string | undefined): { invalid: true | undefined; onBlur: () => void } {
  const [touched, setTouched] = useState(false);
  return {
    invalid: held !== undefined && touched ? true : undefined,
    onBlur: () => {
      setTouched(true);
    },
  };
}

/** RequiredField is a text field the submit is held on. It carries a visible
 * requirement marker beside its label and `aria-required` on its input, and it
 * associates the two by id rather than by wrapping, so the marker stays out of
 * the input's accessible name. When the hold stands on this field, `held`
 * carries the id of the element stating it. */
function RequiredField({
  label,
  value,
  testID,
  held,
  onChange,
}: {
  label: string;
  value: string;
  testID: string;
  held?: string;
  onChange: (next: string) => void;
}) {
  const inputID = useId();
  const { invalid, onBlur } = useHeldInvalid(held);
  return (
    <div className="field">
      <span className="label">
        <label htmlFor={inputID}>{label}</label>{' '}
        <span className="label-required" data-testid={`${testID}-required`}>
          required
        </span>
      </span>
      <input
        id={inputID}
        type="text"
        value={value}
        aria-required="true"
        aria-invalid={invalid}
        aria-describedby={held}
        onBlur={onBlur}
        onChange={(event) => {
          onChange(event.target.value);
        }}
      />
    </div>
  );
}

/** SourceChoice is the source selector. The source types are two exclusive
 * choices that fit on one row, so they are drawn as a segmented control the
 * reader reads both options from rather than as a list they have to open. */
function SourceChoice({ value, onChange }: { value: string; onChange: (next: string) => void }) {
  const options = [
    { id: 'git', label: 'Git repository' },
    { id: 'local', label: 'Local folder' },
  ];
  return (
    <div className="field">
      <span className="label" id="register-source-label">
        Source
      </span>
      <div className="segmented" role="radiogroup" aria-labelledby="register-source-label">
        {options.map((option) => (
          <button
            key={option.id}
            type="button"
            role="radio"
            aria-checked={value === option.id}
            className={value === option.id ? 'segment segment-on' : 'segment'}
            onClick={() => {
              onChange(option.id);
            }}
          >
            {option.label}
          </button>
        ))}
      </div>
    </div>
  );
}

/** VisibilityAxis is one grant, drawn as a card carrying what the grant
 * admits. An axis named alone reads as a term the reader has to already know,
 * and the axes combine, so each card states who it lets in and a selected
 * axis holds the control naming its members. */
function VisibilityAxis({
  name,
  description,
  checked,
  onChange,
  children,
}: {
  name: string;
  description: string;
  checked: boolean;
  onChange: (next: boolean) => void;
  children?: ReactNode;
}) {
  const boxID = useId();
  const descID = useId();
  return (
    <div className={checked ? 'vis-card vis-card-on' : 'vis-card'}>
      <input
        type="checkbox"
        id={boxID}
        checked={checked}
        aria-describedby={descID}
        onChange={(event) => {
          onChange(event.target.checked);
        }}
      />
      <div className="vis-card-body">
        <label className="vis-card-name" htmlFor={boxID}>
          {name}
        </label>
        <p className="vis-card-desc" id={descID}>
          {description}
        </p>
        {checked && children}
      </div>
    </div>
  );
}

/** TokenInput names the members of a selected axis. The parsed members are
 * echoed back as tokens, because a comma-separated line does not show the
 * reader how it was split and a mis-split grant admits the wrong people. Each
 * token removes itself, so a member entered by mistake is dropped from the
 * grant without editing a separator out of the line by hand.
 *
 * An axis carrying a list of known names also draws the picker over them. The
 * field is a text input rather than a wrapping label because the tokens and
 * the picker rows are controls, and a control inside a label steals the
 * label's click. */
function TokenInput({
  label,
  value,
  onChange,
  tokens,
  known,
  held,
}: {
  label: string;
  value: string;
  onChange: (next: string) => void;
  tokens: string[];
  /** held carries the id of the element stating the hold when the submit is
   * held on this field, and is absent otherwise. */
  held?: string;
  /** known is the set of names a value can be checked against. An axis with
   * no such set, or a set that is empty, draws the input alone. */
  known?: string[];
}) {
  const inputID = useId();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const { invalid, onBlur } = useHeldInvalid(held);
  const picker = useGroupPicker(known, value, (next) => {
    onChange(next);
    // A pick leaves the caret where the reader was typing. Focus stays on the
    // row that was clicked otherwise, and the next characters go nowhere.
    inputRef.current?.focus();
  });
  return (
    <div className="field token-input">
      <label className="label" htmlFor={inputID}>
        {label}
      </label>
      <input
        id={inputID}
        ref={inputRef}
        type="text"
        value={value}
        aria-invalid={invalid}
        aria-describedby={held}
        onBlur={onBlur}
        onKeyDown={picker.onKeyDown}
        onChange={(event) => {
          onChange(event.target.value);
        }}
      />
      {tokens.length > 0 && (
        <span className="token-row">
          {tokens.map((token) => (
            <button
              type="button"
              className="token mono token-drop"
              key={token}
              aria-label={`Remove ${token}`}
              onClick={() => {
                onChange(without(tokens, token).join(', '));
              }}
            >
              {token}
              <span aria-hidden="true">✕</span>
            </button>
          ))}
        </span>
      )}
      {known !== undefined && known.length > 0 && (
        <GroupPicker known={known} value={value} matches={picker.matches} at={picker.at} onPick={picker.pick} />
      )}
    </div>
  );
}

/** pickerVisibleRows is how many whole rows the picker shows before the rest
 * scroll. The box holds half a row more than this, so the next row is drawn
 * cut off rather than the list ending on the border with nothing to say it
 * continues. It has to agree with the `--picker-rows` height in `.picker-rows`,
 * which is what actually bounds the box. */
const pickerVisibleRows = 3;

/** useGroupPicker holds the picker's highlighted row and the key handling the
 * input needs to drive it. The picker is a typeahead under a text field, so
 * the keys belong on the field the reader is typing into: reaching a row by
 * tabbing means tabbing back out of the list to keep typing, and the reader
 * who is entering names by hand never leaves the keyboard. ⏎ is consumed
 * whenever a row is highlighted, because the field sits in a form and an
 * uncancelled ⏎ there submits the registration instead of entering the
 * name. */
function useGroupPicker(known: string[] | undefined, value: string, onChange: (next: string) => void) {
  const [highlighted, setHighlighted] = useState(0);
  const matches = known === undefined ? [] : matchGroups(known, value);
  // Typing narrows the list under the highlight, so the stored index can name
  // a row that is no longer drawn. Clamping on read keeps the highlight on a
  // drawn row without re-running state for every keystroke.
  const at = matches.length === 0 ? 0 : Math.min(highlighted, matches.length - 1);
  const pick = (name: string) => {
    onChange(replaceFragment(value, name));
    // The picked name leaves the list, so the highlight returns to the top of
    // what remains rather than to whatever slid into the vacated index.
    setHighlighted(0);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (matches.length === 0) {
      return;
    }
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();
        setHighlighted((at + 1) % matches.length);
        return;
      case 'ArrowUp':
        event.preventDefault();
        setHighlighted((at + matches.length - 1) % matches.length);
        return;
      case 'Enter':
        event.preventDefault();
        pick(matches[at]);
        return;
      default:
        return;
    }
  };
  return { matches, at, pick, onKeyDown };
}

/** GroupPicker is the typeahead the group axis draws under its input. A grant
 * to a group nobody is in admits nobody, and the refusal for it never comes:
 * the registry accepts any name, so a typo is a layer that silently serves no
 * one. The picker states how many known names the part-way entry matches and
 * enters a match on a click, which is the check the form can make before the
 * registration is sent. It renders in flow rather than as an overlay so the
 * dialog does not have to grow around it. */
function GroupPicker({
  known,
  value,
  matches,
  at,
  onPick,
}: {
  known: string[];
  value: string;
  matches: string[];
  /** at is the index of the highlighted row, which the arrow keys move. */
  at: number;
  onPick: (name: string) => void;
}) {
  const query = fragment(value);
  const scrolls = matches.length > pickerVisibleRows;
  const highlightedRef = useRef<HTMLButtonElement | null>(null);
  // The box draws three rows and scrolls the rest, so the highlight can move
  // onto a row nobody can see. Following it keeps the arrow keys legible on a
  // list longer than the box.
  useEffect(() => {
    highlightedRef.current?.scrollIntoView({ block: 'nearest' });
  }, [at]);
  return (
    <div className="picker" data-testid="group-picker">
      <p className="picker-head">
        <span className="label">Groups already granted</span>
        <span className="picker-count" data-testid="group-picker-count">
          {matches.length} of {known.length} match{scrolls && ' · scroll for more'}
        </span>
      </p>
      {matches.length === 0 ? (
        <p className="picker-empty" data-testid="group-picker-empty">
          {query === ''
            ? 'Every group granted elsewhere is already named here.'
            : `No group granted elsewhere matches “${query}”.`}
        </p>
      ) : (
        <>
          <div className={scrolls ? 'picker-rows picker-rows-scrolls' : 'picker-rows'} data-testid="group-picker-rows">
            {matches.map((name, row) => (
              <button
                type="button"
                className={row === at ? 'picker-row picker-row-on mono' : 'picker-row mono'}
                key={name}
                ref={row === at ? highlightedRef : undefined}
                aria-current={row === at ? true : undefined}
                onClick={() => {
                  onPick(name);
                }}
              >
                {name}
              </button>
            ))}
          </div>
          {/* The keys are stated rather than left to be discovered, because
              nothing about a list under a text field says the arrows reach
              it. */}
          <p className="picker-foot" data-testid="group-picker-keys">
            Type to narrow. ↑↓ to move, ⏎ to select.
          </p>
        </>
      )}
    </div>
  );
}

/** consequence states who the selected grants admit, in the terms the reader
 * reviewing the registration cares about. §4.6 unions the axes, so a wider
 * grant subsumes a narrower one and the line says so rather than listing
 * every axis back. */
function consequence(isPublic: boolean, organization: boolean, groups: string[], users: string[]): string {
  const named = [...groups, ...users];
  if (isPublic) {
    return 'Anyone will see this layer, signed in or not.';
  }
  if (organization) {
    return named.length === 0
      ? 'Everyone in this tenant will see this layer.'
      : `Everyone in this tenant will see this layer — the organization grant already covers ${list(named)}.`;
  }
  if (named.length === 0) {
    // Spec: §13.10 / §13.12 — a registration that carries no visibility does
    // not store an ungranted layer. The registry stamps the deployment's
    // fallback, which resolves to public on a standalone with no identity
    // provider, to private once one gates access, and to whatever
    // PODIUM_DEFAULT_LAYER_VISIBILITY names when the operator set it. The
    // form reads none of those, so the line states the rule and points at the
    // registered row, which carries what the registry actually applied.
    return (
      "No grants — the registry stamps this deployment's default visibility, " +
      'which is public on a standalone with no identity provider. The registered row states what it applied.'
    );
  }
  return `Only ${list(named)} will see this layer.`;
}

/** list renders a member list as written English, so the consequence line
 * reads as a sentence rather than as the request body. */
function list(members: string[]): string {
  if (members.length === 1) {
    return members[0];
  }
  return `${members.slice(0, -1).join(', ')} and ${members[members.length - 1]}`;
}

/** layerCapExceeded is the §6.10 code the registry refuses a registration
 * past the per-user layer cap with. Its details carry the limit and the
 * caller's current count. */
const layerCapExceeded = 'quota.layer_count_exceeded';

/** registryUnavailable is the §6.10 code a call that never reached the
 * registry takes, which is the one failure the registry did not answer. */
const registryUnavailable = 'registry.unavailable';

/**
 * RegistrationRefusal presents what the registry refused. The cap refusal is
 * the one the reader can act on, and this is where they created the layer, so
 * it renders the limit and their current count here rather than arriving as
 * the generic failure every other refusal gets.
 *
 * Every other refusal carries a §6.10 envelope the registry wrote, so the
 * banner says the registry refused the registration and names what it did not
 * create. The default title is the wording for the failure the registry did
 * not answer at all, and a refusal titled that way tells the reader the
 * registry never answered when it answered with a code and a message.
 */
function RegistrationRefusal({ refusal }: { refusal: unknown }) {
  if (!(refusal instanceof ApiError) || refusal.code === registryUnavailable) {
    return <ErrorState error={refusal} />;
  }
  if (refusal.code !== layerCapExceeded) {
    return <ErrorState error={refusal} title="The registry refused this registration and no layer was created." />;
  }
  return (
    <div className="banner banner-danger" role="alert" aria-label="Layer limit reached">
      <p className="banner-title">
        You have reached your layer limit — {count(refusal.details.current)} of {count(refusal.details.limit)}.
      </p>
      <p className="mono banner-code">{refusal.label}</p>
      <p>Unregister a layer you no longer read from, or ask an administrator to raise the limit.</p>
    </div>
  );
}

/** count renders a details value the registry sends as a number. A registry
 * that omits it leaves the reader a dash rather than the word "undefined". */
function count(value: unknown): string {
  return typeof value === 'number' ? String(value) : '—';
}
