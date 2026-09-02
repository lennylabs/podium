// What the caller may take on a layer. One predicate, because the server
// composes one: authorizeLayerWrite's two arms
// (pkg/registry/server/layers.go) and authorizeLocalSource's admin arm, which
// applies to register, update, restore, and reingest and to neither
// unregister nor reorder. Its Go mirror is
// TestLayerWriteAuth_UserDefinedOwnerOrAdmin
// (pkg/registry/server/layer_write_auth_test.go), and the two tables are
// meant to be diffable by eye.
//
// Spec: §7.3.1, §13.10

import type { LayerCapabilities } from "../session";

/** LayerOp is the §7.3.1 write operation a control would take. */
export type LayerOp =
  | "register"
  | "update"
  | "restore"
  | "reingest"
  | "unregister"
  | "reorder";

/** LayerTarget is the layer as the operation would name it: the stored record
 * for unregister, restore, reingest, and reorder; for update the stored
 * record's UserDefined and Owner with SourceType omitted and LocalPath taken
 * from the patch; and for register the registration the dialog would build,
 * carrying the class it asks for and the registrant as Owner.
 * LayerRecord satisfies it structurally, so a caller passes a row directly. */
export interface LayerTarget {
  UserDefined?: boolean;
  Owner?: string;
  SourceType?: string;
  LocalPath?: string;
}

/** ownedByCaller is the panel's ownership marker. It is a property of a
 * user-defined row alone: on such a row it compares the row's stored owner
 * against the caller's own subject, and the posture read reports a subject
 * only where one resolves, so a caller with no subject carries no marker on
 * any row. An admin-defined row carries no marker on any value of its stored
 * owner, because the write rule authorizes a tenant admin alone there and
 * that owner names no authorized subject. */
export function ownedByCaller(target: LayerTarget, subject: string): boolean {
  return (
    target.UserDefined === true && subject !== "" && target.Owner === subject
  );
}

/** namesHostPath reports whether the operation's target names a filesystem
 * path on the registry host. It is the server predicate with its Repo
 * disjunct dropped and its git carve-out kept: a stored git layer carrying a
 * local_path is admitted by the server, because the Git transport never reads
 * that path, and a client predicate without the term would hide Reingest and
 * Restore from the non-admin owner who holds them. A repository string that
 * resolves to go-git's file transport does name such a path, and the client
 * deliberately does not mirror that classifier, so a target carrying one is
 * offered the operation and answered by the registry. */
export function namesHostPath(target: LayerTarget): boolean {
  return (
    target.SourceType === "local" ||
    (target.SourceType !== "git" && (target.LocalPath ?? "") !== "")
  );
}

/** newLayerTarget is the registration the dialog would build under an unused
 * ID: user-defined, owned by the registrant, naming no filesystem path. Every
 * reader of the register prediction shares it, so the control that takes the
 * operation and the copy that instructs a reader to press it read one value
 * rather than two statements of the same reduction. On this target mayTake is
 * caps.manage_any_layer || ownedByCaller(target, subject), and ownedByCaller's
 * own empty-subject guard is what makes a caller who resolves none fall
 * through to the admin arm, which is the arm the register handler decides.
 * The dialog's own layer-class control and its Local folder option predict a
 * different registration and build their own target. */
export function newLayerTarget(subject: string): LayerTarget {
  return { UserDefined: true, Owner: subject };
}

/** mayTake reports whether the registry would admit this caller on this
 * operation against this target. Presence of every control that would take a
 * §7.3.1 layer write is decided by it and by nothing else; the present
 * controls are then disabled by the §13.2.1 read-only marker, and a refusal
 * the target's own fields do not settle is drawn on the row it came back on.
 *
 * Spec: §7.3.1 */
export function mayTake(
  op: LayerOp,
  target: LayerTarget,
  caps: LayerCapabilities,
  subject: string,
): boolean {
  const authorized = caps.manage_any_layer || ownedByCaller(target, subject);
  return (
    authorized &&
    (!reads(op) || !namesHostPath(target) || caps.manage_any_layer)
  );
}

/** reads is the local-source rule's call-site table: the operations that read
 * the layer's source, which are the ones the rule guards. unregister and
 * reorder name no path and re-read none, so neither reaches the path term. */
function reads(op: LayerOp): boolean {
  return (
    op === "register" || op === "update" || op === "restore" || op === "reingest"
  );
}
