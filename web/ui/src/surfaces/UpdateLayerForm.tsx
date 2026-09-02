// Updating a layer. The update is a partial patch, so a field left as the
// stored value is sent unchanged and the registry keeps it. The visibility
// axes are offered on an admin-defined layer, which is the class the endpoint
// applies them to. §4.6 fixes a user-defined layer's visibility at
// registration, and the registry ignores an owner or a visibility patch there
// and still answers success, so that class displays its visibility rather
// than editing it.

import type { FormEvent } from "react";
import { useState } from "react";

import { SecretReveal, useSecretAcknowledgement } from "./SecretReveal";
import { members, merge } from "./members";
import { mayTake } from "./layerrights";
import type { LayerCapabilities } from "../session";
import { Badge, ErrorState, Modal } from "../components/primitives";
import type { LayerRecord, LayerSecretResult, LayerUpdate } from "../api";
import { updateLayer } from "../api";

export function UpdateLayerForm({
  layer,
  caps,
  subject,
  readOnly,
  onUpdated,
  onClose,
}: {
  layer: LayerRecord;
  /** caps is what this deployment's layer endpoints admit this caller on.
   * The Local path field patches a filesystem path on the registry host,
   * which §7.3.1 authorizes to a tenant admin alone, so the field is present
   * only where the call admits it while the form itself stays open to the
   * owner for the rest of the patch. */
  caps: LayerCapabilities;
  subject: string;
  readOnly: boolean;
  onUpdated: () => void;
  onClose: () => void;
}) {
  const git = layer.SourceType === "git";
  const [ref, setRef] = useState(layer.Ref ?? "");
  const [root, setRoot] = useState(layer.Root ?? "");
  const [localPath, setLocalPath] = useState(layer.LocalPath ?? "");
  const [policy, setPolicy] = useState(layer.force_push_policy ?? "tolerant");
  const [rotate, setRotate] = useState(false);
  const editableVisibility = layer.UserDefined !== true;
  const [isPublic, setPublic] = useState(layer.Public === true);
  const [organization, setOrganization] = useState(layer.Organization === true);
  // The members already granted on an axis are displayed rather than edited,
  // because the endpoint grants on each axis and withdraws on none: a field
  // holding them would accept a deletion the registry discards while still
  // answering success, which reads to an operator as an access narrowing that
  // never happened. The field beside them names the members to add.
  const grantedGroups = layer.Groups ?? [];
  const grantedUsers = layer.Users ?? [];
  // An axis the layer already grants is state rather than a choice, and it is
  // drawn as such. Offered as a checkbox it was operable in appearance and
  // inert in fact: the click changed nothing, the form still answered "Layer
  // updated", and the row still carried the axis.
  const grantedAxes = [
    layer.Public === true ? "public" : "",
    layer.Organization === true ? "organization" : "",
  ].filter((axis) => axis !== "");
  const [groups, setGroups] = useState("");
  const [users, setUsers] = useState("");
  const [result, setResult] = useState<LayerSecretResult | null>(null);
  const secret = useSecretAcknowledgement();
  const [refusal, setRefusal] = useState<unknown>(null);
  // A patch carrying a rotation issues a fresh webhook secret on every call,
  // and the reveal presents that secret as shown once. A second patch sent
  // while the first is still open rotates again and replaces the value the
  // reader is copying, leaving them holding a secret the registry has already
  // retired. The form holds the write while one is open, on the same terms as
  // the row's Reingest control.
  const [pending, setPending] = useState(false);
  // The field would patch local_path, so the target it predicts names a
  // filesystem path whatever the reader has typed so far. The rest of the
  // patch names none, which is what keeps the form itself open to the
  // non-admin owner of a local layer.
  const mayNamePath = mayTake(
    "update",
    {
      UserDefined: layer.UserDefined,
      Owner: layer.Owner,
      LocalPath: layer.LocalPath === undefined || layer.LocalPath === "" ? "a path" : layer.LocalPath,
    },
    caps,
    subject,
  );

  // send is held apart from the form's submit handler so a refused patch can
  // be re-issued from the refusal itself, which is the treatment every other
  // refused write on the panel carries.
  const send = () => {
    if (pending) {
      return;
    }
    const patch: LayerUpdate = git
      ? { ref, root, force_push_policy: policy, rotate_webhook_secret: rotate }
      : mayNamePath
        ? { local_path: localPath, root }
        : { root };
    if (editableVisibility) {
      // Each axis the patch carries grants, and an axis it omits keeps its
      // stored value, so the form sends an axis the reader turned on and the
      // stored members plus the ones they added.
      patch.public = isPublic;
      patch.organization = organization;
      patch.groups = merge(grantedGroups, members(groups));
      patch.users = merge(grantedUsers, members(users));
    }
    setPending(true);
    updateLayer(layer.ID, patch).then(
      (next) => {
        setPending(false);
        setRefusal(null);
        setResult(next);
        onUpdated();
      },
      (err: unknown) => {
        setPending(false);
        setResult(null);
        setRefusal(err);
      },
    );
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    send();
  };

  const done = () => {
    setResult(null);
    onClose();
  };

  if (result !== null) {
    // A rotation returns the fresh secret once, on the same terms as
    // registration, so the reveal is the one the register flow uses, and
    // until the reader acknowledges it the dialog closes only through the
    // reveal's own acknowledgement. Escape, the scrim, and the close control
    // would discard a credential the reader can then recover only by rotating
    // it again. The acknowledgement retires that hold.
    return (
      <Modal
        title="Layer updated"
        onClose={done}
        dismissible={secret.dismissible(result)}
      >
        <SecretReveal
          result={result}
          outcome={`Layer ${layer.ID} is updated.`}
          acknowledged={secret.acknowledged}
          onAcknowledge={secret.setAcknowledged}
          onDone={done}
        />
      </Modal>
    );
  }

  return (
    // The update is reviewed before it is sent, on the same terms as the
    // register and the unregister writes, so it is presented over the panel
    // rather than pushed into the row that opened it. Left inside the actions
    // cell it took that column's width, which is too narrow for a filesystem
    // path, and grew the row enough to reflow its neighbours.
    <Modal title={`Edit ${layer.ID}`} onClose={onClose}>
      <form
        className="register-form modal-form"
        aria-label={`Update ${layer.ID}`}
        onSubmit={submit}
      >
        <div className="modal-body">
          {git ? (
            <>
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
              <label className="field">
                <span className="label">Force-push policy</span>
                <select
                  value={policy}
                  onChange={(event) => {
                    setPolicy(event.target.value);
                  }}
                >
                  <option value="tolerant">
                    Tolerant: ingest a rewritten history
                  </option>
                  <option value="strict">
                    Strict: reject a rewritten history
                  </option>
                </select>
              </label>
            </>
          ) : (
            mayNamePath && (
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
            )
          )}
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
          {editableVisibility ? (
            <fieldset className="field">
              <legend className="label">Visibility</legend>
              <GrantedAxes axes={grantedAxes} />
              {layer.Public !== true && (
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
              )}
              {layer.Organization !== true && (
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
              )}
              <GrantedMembers label="Groups granted" members={grantedGroups} />
              <label className="field">
                <span className="label">
                  Group names to add, separated by commas
                </span>
                <input
                  type="text"
                  value={groups}
                  onChange={(event) => {
                    setGroups(event.target.value);
                  }}
                />
              </label>
              <GrantedMembers label="Users granted" members={grantedUsers} />
              <label className="field">
                <span className="label">
                  User identifiers to add, separated by commas
                </span>
                <input
                  type="text"
                  value={users}
                  onChange={(event) => {
                    setUsers(event.target.value);
                  }}
                />
              </label>
            </fieldset>
          ) : (
            <div className="field">
              <span className="label">Visibility</span>
              <Badge tone="quiet">you alone</Badge>
              <p className="quiet">
                A layer of your own is fixed to you at registration and cannot
                be widened.
              </p>
            </div>
          )}
          {/* Only a git source carries a webhook secret, so the control stays on
            the row of a layer that has none and reports why it cannot be
            taken rather than disappearing between source types. */}
          <label>
            <input
              type="checkbox"
              checked={rotate}
              disabled={!git}
              onChange={(event) => {
                setRotate(event.target.checked);
              }}
            />
            Rotate the webhook secret
          </label>
          {!git && (
            <p className="quiet">Only a git layer carries a webhook secret.</p>
          )}
          {rotate && (
            <p className="quiet">
              The new secret is shown once. The old one stops working
              immediately.
            </p>
          )}
          {refusal !== null && (
            <>
              <ErrorState error={refusal} write onRetry={send} />
              <button
                type="button"
                onClick={() => {
                  setRefusal(null);
                }}
              >
                Dismiss
              </button>
            </>
          )}
        </div>
        <div className="modal-foot">
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className="button primary"
            disabled={readOnly || pending}
            aria-busy={pending || undefined}
          >
            Save changes
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** GrantedAxes displays the axes the layer already grants, in the fixed order
 * the panel's own visibility column uses, with the sentence that says why they
 * cannot be taken back beside them. The endpoint grants on each axis and
 * withdraws on none, so an axis already stored is not a control here: it is
 * stated where the reader looking for the axis lands, at the top of the
 * fieldset, rather than under the fields below it where the dialog's default
 * scroll position leaves it out of view.
 *
 * Spec: §4.6
 */
function GrantedAxes({ axes }: { axes: readonly string[] }) {
  if (axes.length === 0) {
    return null;
  }
  return (
    <div className="field">
      <span className="label">Axes granted</span>
      <span className="visibility-markers" aria-label="Axes granted">
        {axes.map((axis) => (
          <Badge key={axis} tone="grant">
            {axis}
          </Badge>
        ))}
      </span>
      <p className="quiet">
        An axis already granted stays granted. Unregister the layer to withdraw
        it.
      </p>
    </div>
  );
}

/** GrantedMembers displays the members an axis already carries. They are drawn
 * as tokens rather than as a value in the field beside them, because the
 * registry withdraws no grant: a removable control here would report success
 * on a deletion it discarded.
 *
 * Spec: §4.6
 */
function GrantedMembers({
  label,
  members: granted,
}: {
  label: string;
  members: readonly string[];
}) {
  if (granted.length === 0) {
    return null;
  }
  return (
    <div className="field">
      <span className="label">{label}</span>
      <span className="token-row" aria-label={label}>
        {granted.map((member) => (
          <span className="token mono" key={member}>
            {member}
          </span>
        ))}
      </span>
    </div>
  );
}
