import { describe, expect, it } from 'vitest';

import type { LayerOp, LayerTarget } from './surfaces/layerrights';
import { mayTake, newLayerTarget } from './surfaces/layerrights';
import { capabilitiesOf } from './session';

// The client's prediction of the two server gates: the layer-write rule, whose
// Go mirror is TestLayerWriteAuth_UserDefinedOwnerOrAdmin
// (pkg/registry/server/layer_write_auth_test.go), and the local-source rule
// over register, update, restore, and reingest. The table below is keyed on
// what the rule branches on rather than on the control that reads it, so the
// two tables are diffable by eye.
// Spec: §7.3.1, §13.10
describe('mayTake', () => {
  const closed = { manage_any_layer: false };
  const admin = { manage_any_layer: true };
  const ops: LayerOp[] = [
    'register',
    'update',
    'restore',
    'reingest',
    'unregister',
    'reorder',
  ];

  /** ownGit is a user-defined layer alice owns whose source reads no host
   * path. Every operation is admitted on it for her. */
  const ownGit: LayerTarget = {
    UserDefined: true,
    Owner: 'alice@acme.com',
    SourceType: 'git',
  };
  /** fileTransportRow is a stored record whose repository string resolves to
   * go-git's file transport. LayerRecord satisfies LayerTarget structurally
   * and carries Repo, which the client deliberately does not classify. */
  const fileTransportRow = {
    UserDefined: true,
    Owner: 'alice@acme.com',
    SourceType: 'git',
    Repo: '/srv/other-tenant',
  };
  /** ownLocal is the same layer registered against a directory on the
   * registry host, which is what the local-source rule guards. */
  const ownLocal: LayerTarget = {
    UserDefined: true,
    Owner: 'alice@acme.com',
    SourceType: 'local',
    LocalPath: '/Users/alice/registry',
  };

  it('mirrors the server arms over the caller, the target, and the operation', () => {
    const cases: {
      name: string;
      target: LayerTarget;
      caps: { manage_any_layer: boolean };
      subject: string;
      want: LayerOp[];
    }[] = [
      // The layer-write rule's four callers over a target that names no host
      // path: the stored owner and a tenant admin are authorized, and a
      // different verified subject and a caller resolving no subject are not.
      { name: 'owner', target: ownGit, caps: closed, subject: 'alice@acme.com', want: ops },
      { name: 'other-subject', target: ownGit, caps: closed, subject: 'bob@acme.com', want: [] },
      { name: 'no-subject', target: ownGit, caps: closed, subject: '', want: [] },
      { name: 'admin', target: ownGit, caps: admin, subject: 'bob@acme.com', want: ops },
      // An admin-defined layer carries no owner any caller matches, whatever
      // its stored Owner field says.
      {
        name: 'admin-defined row, its stored owner',
        target: { UserDefined: false, Owner: 'alice@acme.com', SourceType: 'git' },
        caps: closed,
        subject: 'alice@acme.com',
        want: [],
      },
      // A record carrying neither key is admin-defined and unowned.
      { name: 'bare record', target: { SourceType: 'git' }, caps: closed, subject: 'alice@acme.com', want: [] },
      // The local-source rule: the four operations that read the source are
      // refused on a host path, and unregister and reorder name no path and
      // re-read none, so they stay with the write rule alone.
      {
        name: 'owner of a local layer',
        target: ownLocal,
        caps: closed,
        subject: 'alice@acme.com',
        want: ['unregister', 'reorder'],
      },
      { name: 'admin on a local layer', target: ownLocal, caps: admin, subject: 'bob@acme.com', want: ops },
      // The update target carries the record's class and owner with the
      // patch's fields for the rest. A patch that names no path is admitted
      // on the same row the reingest above is refused on, which is what keeps
      // Edit present while the Local path field inside it is withheld.
      {
        name: 'owner patching a local layer without its path',
        target: { UserDefined: true, Owner: 'alice@acme.com' },
        caps: closed,
        subject: 'alice@acme.com',
        want: ops,
      },
      {
        name: 'owner patching a local layer with its path',
        target: { UserDefined: true, Owner: 'alice@acme.com', LocalPath: '/Users/alice/registry' },
        caps: closed,
        subject: 'alice@acme.com',
        want: ['unregister', 'reorder'],
      },
      // A git layer is classified on its repository string alone, which the
      // client does not classify, so a repository that resolves to the file
      // transport is offered the operation and answered by the registry.
      {
        name: 'owner of a git layer whose repository is a host path',
        target: fileTransportRow,
        caps: closed,
        subject: 'alice@acme.com',
        want: ops,
      },
      // A git layer carrying a stored path is admitted: the Git transport
      // never reads that path, and the server admits the same layer.
      {
        name: 'owner of a git layer carrying a stored path',
        target: {
          UserDefined: true,
          Owner: 'alice@acme.com',
          SourceType: 'git',
          LocalPath: '/tmp/stale',
        },
        caps: closed,
        subject: 'alice@acme.com',
        want: ops,
      },
      // A custom source type carrying a path is classified on the path, the
      // way the server classifies it.
      {
        name: 'owner of a custom-source layer carrying a path',
        target: {
          UserDefined: true,
          Owner: 'alice@acme.com',
          SourceType: 'oci',
          LocalPath: '/tmp/oci',
        },
        caps: closed,
        subject: 'alice@acme.com',
        want: ['unregister', 'reorder'],
      },
      // The registration the dialog would build under an unused ID. A caller
      // who resolves a subject owns it; a caller who resolves none falls
      // through to the admin arm, which is the arm the register handler
      // decides.
      { name: 'registration by alice', target: newLayerTarget('alice@acme.com'), caps: closed, subject: 'alice@acme.com', want: ops },
      { name: 'registration by no caller', target: newLayerTarget(''), caps: closed, subject: '', want: [] },
      { name: 'registration on a registry authenticating none', target: newLayerTarget(''), caps: admin, subject: '', want: ops },
      // The registration the dialog's class control would build. It carries
      // no owner the caller matches, so it reduces to the admin arm.
      {
        name: 'admin-defined registration by an authenticated non-admin',
        target: { UserDefined: false, Owner: 'alice@acme.com' },
        caps: closed,
        subject: 'alice@acme.com',
        want: [],
      },
      // The registration the dialog's Local folder option would build.
      {
        name: 'local registration by an authenticated non-admin',
        target: { UserDefined: true, Owner: 'alice@acme.com', SourceType: 'local' },
        caps: closed,
        subject: 'alice@acme.com',
        want: ['unregister', 'reorder'],
      },
    ];
    for (const c of cases) {
      const got = ops.filter((op) => mayTake(op, c.target, c.caps, c.subject));
      expect(got, c.name).toEqual(c.want);
    }
  });

  // The reorder request names the moved row's own class block and the handler
  // refuses the whole call on the first row it cannot write, so the block is
  // settled with every. It carries no condition on the block's length.
  it('settles a reorder over every row of the block the move would name', () => {
    const mine: LayerTarget[] = [
      { UserDefined: true, Owner: 'alice@acme.com' },
      { UserDefined: true, Owner: 'alice@acme.com', SourceType: 'local', LocalPath: '/tmp/x' },
    ];
    const mixed: LayerTarget[] = [...mine, { UserDefined: false, Owner: 'ops@acme.com' }];
    const settle = (block: LayerTarget[]): boolean =>
      block.every((row) => mayTake('reorder', row, closed, 'alice@acme.com'));
    expect(settle(mine)).toBe(true);
    expect(settle(mixed)).toBe(false);
    expect(settle([mine[0]])).toBe(true);
  });
});

// The closed default. A read that did not answer and an answered read that
// carried no object both report every member false, so no call site has to
// remember it.
// Spec: §7.3.4
describe('capabilitiesOf', () => {
  it('reports every member false where the read answered nothing', () => {
    expect(capabilitiesOf(null)).toEqual({ manage_any_layer: false });
    expect(
      capabilitiesOf({
        identity_provider_configured: true,
        public_mode: false,
        browser_auth: { enabled: false },
      }),
    ).toEqual({ manage_any_layer: false });
  });

  it('reports what the read carried', () => {
    expect(
      capabilitiesOf({
        identity_provider_configured: false,
        public_mode: false,
        browser_auth: { enabled: false },
        layer_capabilities: { manage_any_layer: true },
      }),
    ).toEqual({ manage_any_layer: true });
  });
});
