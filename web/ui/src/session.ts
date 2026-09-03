// The §7.3.4 posture read and the posture-keyed rendering rules web/DESIGN.md
// states. The browser can observe neither the deployment's identity posture
// nor its own resolved subject, so every rule here keys on this read and on
// nothing the page infers from a catalog response.

import { paths } from './api';

/** LayerCapabilities is what this deployment's layer endpoints admit this
 * caller on. It carries one member per gate the client predicts, and its Go
 * mirror is the wire type in pkg/registry/server/webui_session.go. */
export interface LayerCapabilities {
  /** manage_any_layer reports whether the layer endpoints admit this caller
   * on the §4.7.2 admin arm, which is the arm that authorizes a write to a
   * layer the caller does not own and an operation naming a filesystem path
   * on the registry host. */
  manage_any_layer: boolean;
}

/** SessionPosture is the posture read's body. The read reports the
 * deployment's identity posture, the caller's own resolved subject and email,
 * and what this deployment's layer endpoints admit that caller on, and
 * nothing else. */
export interface SessionPosture {
  identity_provider_configured: boolean;
  public_mode: boolean;
  browser_auth: {
    enabled: boolean;
    /** Reported only where the flow is enabled, because the routes are
     * registered only there. The page navigates to what the read reports
     * rather than to a path spelled in the bundle. */
    sign_in_path?: string;
    sign_out_path?: string;
  };
  /** Present only where a subject resolves. */
  subject?: string;
  /** Present only where the configured identity provider recorded one for
   * this caller. The identity cluster renders it in place of the
   * provider-chosen subject, which on many deployments is an opaque
   * identifier a reader cannot recognize as their own. */
  email?: string;
  /** The field is optional on the type because a response from an older
   * registry carries none. capabilitiesOf is what turns that into a closed
   * default, and every reader goes through it. */
  layer_capabilities?: LayerCapabilities;
}

/** readSession takes the posture read. A registry serving no web UI never
 * registers the path, so the read can fail, and the caller renders the
 * anonymous presentation for that arm. */
export async function readSession(): Promise<SessionPosture> {
  const response = await fetch(paths.session);
  if (!response.ok) {
    throw new Error(`the posture read answered ${response.status}`);
  }
  return (await response.json()) as SessionPosture;
}

/** AuthControl is what the shell renders for the caller's posture. */
export type AuthControl =
  | { kind: 'none' }
  | { kind: 'sign-in'; path: string }
  | { kind: 'sign-out'; path: string };

/**
 * authControl applies the sign-in control rule. Both conjuncts are required
 * on each of the first two rows: the flow enabled with no subject renders a
 * sign-in navigation to the path the read reports, the flow enabled with a
 * subject renders sign-out, and a deployment running no browser flow renders
 * neither control on any value of subject. A read that did not answer leaves
 * the page holding no value for either key, so it renders neither.
 */
export function authControl(posture: SessionPosture | null): AuthControl {
  if (posture === null || !posture.browser_auth.enabled) {
    return { kind: 'none' };
  }
  if (posture.subject !== undefined && posture.subject !== '') {
    const path = posture.browser_auth.sign_out_path;
    return path === undefined ? { kind: 'none' } : { kind: 'sign-out', path };
  }
  const path = posture.browser_auth.sign_in_path;
  return path === undefined ? { kind: 'none' } : { kind: 'sign-in', path };
}

/**
 * expiryControl is what a surface offers a caller whose catalog read was
 * refused. The caller held a subject when the page loaded and holds none
 * the registry will verify now, so the control that recovers the state is
 * sign-in rather than the sign-out authControl renders for the same posture.
 * The sign-in control rule's third row bounds it: a deployment reporting the
 * browser flow disabled renders no authentication control at all, and the
 * surface states what it offers in its place. A read that did not answer
 * reports no path to navigate to, so it renders no control either; the two
 * arms differ in what the surface may say about the deployment, which is why
 * the surface reads the posture rather than this result alone.
 */
export function expiryControl(posture: SessionPosture | null): AuthControl {
  if (posture === null || !posture.browser_auth.enabled) {
    return { kind: 'none' };
  }
  const path = posture.browser_auth.sign_in_path;
  return path === undefined ? { kind: 'none' } : { kind: 'sign-in', path };
}

/**
 * CatalogScope is the arm of the catalog-scope rule the page renders.
 * "refused" is ordered ahead of the other two: where a catalog read is
 * refused because the caller's identity could not be verified, the caller has
 * no anonymous view at all and the refused state stands in place of the
 * catalog. "public-subset" carries the constraint that the page states
 * nothing that would be false on a registry configured with an identity
 * provider whose label the process does not recognise, so it asserts neither
 * that artifacts were withheld nor that hidden artifacts exist. "whole" is
 * every other combination of the two keys, and it carries no framing.
 */
export type CatalogScope = 'refused' | 'public-subset' | 'whole';

/**
 * catalogScope applies the catalog-scope rule. It takes the posture read,
 * which is null where the read did not answer, and whether the catalog read
 * was refused for an unverifiable identity.
 */
export function catalogScope(posture: SessionPosture | null, identityRefused: boolean): CatalogScope {
  if (identityRefused) {
    return 'refused';
  }
  if (posture === null) {
    // The page holds neither key. It presents what the catalog read
    // returned under the constraint the public-subset arm carries.
    return 'public-subset';
  }
  if (posture.identity_provider_configured && !posture.public_mode) {
    return 'public-subset';
  }
  return 'whole';
}

/**
 * capabilitiesOf reads the caller's layer capabilities off the posture,
 * returning every member false where the read did not answer or carried no
 * object. It takes the nullable posture every other rule in this file takes,
 * so no call site has to remember the closed default.
 *
 * The two states it collapses onto one value are a read that did not answer
 * and an answered read that resolved no caller: both callers are refused the
 * same operations. The two empty states are where the two read differently,
 * and the shell derives a separate postureAnswered boolean for them.
 */
export function capabilitiesOf(posture: SessionPosture | null): LayerCapabilities {
  return { manage_any_layer: posture?.layer_capabilities?.manage_any_layer === true };
}

/** isSignedIn reports whether the read resolved a subject for this caller.
 * The read also reports whether this deployment's layer endpoints admit this
 * caller on the §4.7.2 admin arm, which capabilitiesOf carries. It reports no
 * role name and no grant table, so no rule here predicts either. */
export function isSignedIn(posture: SessionPosture | null): boolean {
  return posture !== null && posture.subject !== undefined && posture.subject !== '';
}
