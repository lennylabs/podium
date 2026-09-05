// Updating a layer. The update is a partial patch, so a field left as the
// stored value is sent unchanged and the registry keeps it. The visibility
// axes are offered on an admin-defined layer, which is the class the endpoint
// applies them to: §7.3.1 applies each visibility member the patch carries and
// keeps the stored value of each member it omits, so an axis turned off and a
// member removed here are both withdrawn. §4.6 fixes a user-defined layer's
// visibility at registration, and §7.3.1 refuses a patch that asserts an owner
// or a visibility member differing from the stored value against a stored
// user-defined layer with `400 registry.invalid_argument` carrying
// `details.constraint: "immutable_visibility"`, so that class displays its
// visibility rather than editing it.

import type { FormEvent } from "react";
import { useState } from "react";

import { SecretReveal, useSecretAcknowledgement } from "./SecretReveal";
import { members, without } from "./members";
import { TokenInput } from "./RegisterLayerForm";
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
  const git = layer.source_type === "git";
  const [ref, setRef] = useState(layer.ref ?? "");
  const [root, setRoot] = useState(layer.root ?? "");
  const [localPath, setLocalPath] = useState(layer.local_path ?? "");
  const [policy, setPolicy] = useState(layer.force_push_policy ?? "tolerant");
  const [rotate, setRotate] = useState(false);
  const editableVisibility = layer.user_defined !== true;
  const [isPublic, setPublic] = useState(layer.public === true);
  const [organization, setOrganization] = useState(layer.organization === true);
  const groupMembers = useMemberList(layer.groups ?? []);
  const userMembers = useMemberList(layer.users ?? []);
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
      user_defined: layer.user_defined,
      owner: layer.owner,
      local_path: layer.local_path === undefined || layer.local_path === "" ? "a path" : layer.local_path,
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
      // The patch carries every visibility member, so the registry applies
      // each one: an axis the reader turned off is withdrawn, and a member
      // list is stored as the reader left it.
      patch.public = isPublic;
      patch.organization = organization;
      patch.groups = groupMembers.tokens;
      patch.users = userMembers.tokens;
    }
    setPending(true);
    updateLayer(layer.id, patch).then(
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
          outcome={`Layer ${layer.id} is updated.`}
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
    <Modal title={`Edit ${layer.id}`} onClose={onClose}>
      <form
        className="register-form modal-form"
        aria-label={`Update ${layer.id}`}
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
              <TokenInput
                label="Group names, separated by commas"
                value={groupMembers.line}
                onChange={groupMembers.setLine}
                tokens={groupMembers.tokens}
                onRemove={groupMembers.remove}
              />
              <TokenInput
                label="User identifiers, separated by commas"
                value={userMembers.line}
                onChange={userMembers.setLine}
                tokens={userMembers.tokens}
                onRemove={userMembers.remove}
              />
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

/** useMemberList holds one visibility member list across the dialog: the
 * members the layer already carries, held as the array the record supplied,
 * and the line the reader adds more on.
 *
 * The stored members are held apart from the line rather than joined into it,
 * because the patch replaces the list rather than adding to it and the join is
 * not reversible: a member carrying a comma splits into two on the way back,
 * and one carrying padding returns trimmed. Nothing constrains a member's
 * characters, so a DN-style group name is storable, and an unedited dialog
 * that re-parsed the line would withdraw that grant and grant to names nobody
 * authorized. Held as an array, an untouched dialog sends the stored list
 * verbatim, which §7.3.1 applies as no change at all. */
function useMemberList(stored: readonly string[]) {
  const [kept, setKept] = useState<string[]>([...stored]);
  const [line, setLine] = useState("");
  // A member already stored is dropped from the additions, so the token row
  // draws it once and the patch names it once.
  const added = members(line).filter((member) => !kept.includes(member));
  return {
    line,
    setLine,
    /** tokens is the list the patch carries: the stored members that survive
     * the reader's removals, followed by the ones they added. */
    tokens: [...kept, ...added],
    /** remove takes one member back from both holdings at once. Dropping it
     * from `kept` alone leaves the same name in the line free to re-enter
     * through `added`, which redraws the token and re-grants the member in the
     * patch, so a withdrawal the reader performed would be discarded while the
     * dialog answered success. Rewriting the line is harmless when the token
     * is absent from it. */
    remove: (token: string) => {
      setKept(without(kept, token));
      setLine(without(members(line), token).join(", "));
    },
  };
}
