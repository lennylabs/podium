// The layer panel: the only surface with write operations. The list read
// hands the panel every layer stored under the tenant and no response reports
// that the caller holds the administrator role, so the panel predicts no
// outcome. It renders its write operations on every row and presents whatever
// refusal a write receives.
//
// The panel is rendered for every caller on every deployment, including a
// caller who resolves no subject. A standalone registry authenticates nobody
// and treats the local operator as the administrator, and the panel is the
// point of that deployment.

import type { KeyboardEvent, RefObject } from "react";
import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

import { grantedGroups } from "./members";
import { erasesOn, recoveryDays } from "./recovery";
import { RegisterLayerForm } from "./RegisterLayerForm";
import type { ReingestOutcome, ReingestState } from "./ReingestControl";
import {
  idleReingest,
  ReingestButton,
  ReingestRunReport,
  ReingestStatus,
  reingestRefusal,
} from "./ReingestControl";
import { UpdateLayerForm } from "./UpdateLayerForm";
import {
  Badge,
  Banner,
  EmptyState,
  ErrorState,
  Loading,
  Modal,
} from "../components/primitives";
import { SourceCell } from "../components/SourceCell";
import { takeFocus, usePopupDismiss } from "../components/focus";
import type { BreakGlass, LayerRecord } from "../api";
import {
  ingestRef,
  shortRef,
  visibilityMarkers,
  visibilitySummary,
} from "./layerfacts";
import {
  listDeletedLayers,
  listLayers,
  readQuota,
  reingestLayer,
  reorderLayers,
  unregisterLayer,
} from "../api";
import { deletedLayersHref } from "../route";
import { since } from "../time";
import type { Async } from "../useAsync";
import { useAsync, useReachReport } from "../useAsync";

/** RecoverableCount states how much the deleted-layer read found, beside the
 * link that opens it. A read that failed states that instead of a count: the
 * failed read and a read that found nothing both hold no layers, and drawing
 * both as no badge reports a registry that did not answer as a registry
 * holding nothing on its way to being erased. */
function RecoverableCount({ read }: { read: Async<LayerRecord[]> }) {
  if (read.error !== null) {
    return <Badge tone="count">?</Badge>;
  }
  if (read.value === null || read.value.length === 0) {
    return null;
  }
  return <Badge tone="count">{String(read.value.length)}</Badge>;
}

/** Refusal is a write the registry refused, held with the write itself so the
 * row can re-issue exactly what was attempted. */
interface Refusal {
  error: unknown;
  retry: () => void;
}

/** Run is one "Reingest all" press: when it started, what each layer
 * answered, and when the last answer came back. The run reports nothing
 * until it ends, because the registry runs each layer's pipeline inside its
 * own request and a run that is still issuing them has no result to state. */
interface Run {
  startedAt: number;
  outcomes: ReingestOutcome[];
  finishedAt: number | null;
}

/** Outcome is what the last committed write did, and whether the reader can
 * see that it happened without being told. A reorder leaves the rows in their
 * new order on the page, so it is announced alone. An unregister takes its row
 * away and moves the layer into a recovery window that ends, and neither the
 * window nor where the layer went is stated anywhere else on the surface, so
 * it is drawn as well as announced. */
type Outcome = { text: string; visible: boolean };

const noOutcome: Outcome = { text: "", visible: false };

/** announced holds an outcome the page already shows the effect of. */
function announced(text: string): Outcome {
  return { text, visible: false };
}

/** drawn holds an outcome the reader is left with no on-screen trace of. */
function drawn(text: string): Outcome {
  return { text, visible: true };
}

export function LayerPanel({
  subject,
  readOnly,
  onCatalogChange,
  onReach,
}: {
  subject: string;
  readOnly: boolean;
  /** onCatalogChange tells the shell that a write moved what the catalog
   * holds, so the counts the sidebar footer states are re-read. The panel
   * owns no part of that footer, and without the signal it keeps the figure
   * the page loaded with for the rest of the session. */
  onCatalogChange: () => void;
  /** onReach tells the shell that this read answered, so a shell read that
   * failed during the same outage is re-issued rather than leaving the
   * sidebar stating an outage the panel beside it has come back from. */
  onReach: () => void;
}) {
  const layers = useAsync(() => listOrdered(), []);
  // The recoverable list is read for its count alone here. The section it
  // opens reads the list itself, so the panel holds no copy of what that
  // section renders.
  const recoverable = useAsync(() => listDeletedLayers(), []);
  useReachReport(!layers.loading && layers.error === null, onReach);
  const [registering, setRegistering] = useState(false);
  // A write's refusal is drawn on the row it was attempted on. The panel
  // holds the map because a reorder is committed by a drop on another row
  // and its refusal belongs to the row that moved.
  const [refusals, setRefusals] = useState<Record<string, Refusal | null>>({});
  // Each row's reingest state is driven by that row's own response, which is
  // what lets the fan-out leave every row it has not heard from untouched.
  const [reingest, setReingest] = useState<Record<string, ReingestState>>({});
  // The fan-out is one press, so it answers with one report. Each layer's
  // answer is collected here and the whole run is presented once it ends,
  // rather than resolving every layer into a dialog of its own.
  const [run, setRun] = useState<Run | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);
  const [over, setOver] = useState<string | null>(null);
  // What the last committed write did, held for the live region below the
  // table. A reorder reports itself only by the rows swapping places, which
  // is no report at all to the operator driving the handle from the keyboard,
  // and an unregister takes its own row away, so neither leaves anything on
  // the page that names what happened.
  const [outcome, setOutcome] = useState<Outcome>(noOutcome);
  // The heading is where focus lands when a write removes the control it was
  // started from. The row's controls go with the row, and focus left on the
  // document body puts the reader back at the top of the page.
  const heading = useRef<HTMLHeadingElement>(null);

  /** reloadPanel re-reads every read the panel owns. The panel's retry runs
   * it, because the outage that refused the layer list refused the deleted
   * list beside it, and reloading the rows alone leaves the recoverable count
   * reading as if nothing were recoverable for the rest of the session. */
  const reloadPanel = () => {
    layers.reload();
    recoverable.reload();
  };

  /** runReport is the finished fan-out's report, built before the panel's
   * read guards. The run ends with a reload, and an outage that began while
   * the fan-out was issuing requests fails that reload and puts the panel
   * into its error state; a report rendered only under that guard is
   * unmounted by it, and a press that issued a request per layer would then
   * state nothing about what any of them answered. */
  const runReport =
    run !== null && run.finishedAt !== null ? (
      <ReingestRunReport
        outcomes={run.outcomes}
        startedAt={run.startedAt}
        finishedAt={run.finishedAt}
        onDone={() => {
          setRun(null);
        }}
      />
    ) : null;

  // The loading state stands in for the panel on the first read alone. A
  // write reloads the list, and the reload reports loading again, so swapping
  // the whole panel out here would unmount the form that issued the write and
  // discard the one-time webhook secret its response carried. The panel holds
  // the rows it already has until the reload answers.
  if (layers.loading && layers.value === null) {
    return <Loading label="Loading the layers." />;
  }
  if (layers.error !== null) {
    return (
      <>
        {runReport}
        <ErrorState error={layers.error} onRetry={reloadPanel} />
      </>
    );
  }
  const rows = layers.value ?? [];

  // One in-flight guard covers the whole surface. The registry runs the §7.3.1
  // ingest pipeline inside the request, so two open requests for one layer run
  // that pipeline twice over the same source at once. While a row's own
  // reingest is open the fan-out is held, and while the fan-out is open every
  // row trigger is held, which also keeps the fan-out from overwriting a row
  // state the reader has not read yet.
  const runActive = run !== null && run.finishedAt === null;
  const reingesting =
    runActive ||
    Object.values(reingest).some((state) => state.kind === "running");

  /** afterWrite re-reads everything a layer write moves: the panel's own
   * rows, and the shell's catalog read behind the sidebar tree and the footer
   * counts. Every write path goes through it, because a register, an
   * unregister, a restore, and a reingest each move what the catalog holds. */
  const afterWrite = () => {
    layers.reload();
    onCatalogChange();
  };

  const recordRefusal = (id: string, err: unknown, retry: () => void) => {
    setRefusals((prev) => ({ ...prev, [id]: { error: err, retry } }));
  };
  const clearRefusal = (id: string) => {
    setRefusals((prev) => ({ ...prev, [id]: null }));
  };
  const setRowReingest = (id: string, state: ReingestState) => {
    setReingest((prev) => ({ ...prev, [id]: state }));
  };

  /** runReingest drives one layer's reingest and moves that layer's row
   * alone. The pipeline runs inside the request, so nothing is reported
   * until it returns and the row shows what its own response carried. */
  const runReingest = async (
    id: string,
    breakGlass?: BreakGlass,
  ): Promise<void> => {
    const startedAt = Date.now();
    setRowReingest(id, { kind: "running" });
    try {
      const summary = await reingestLayer(id, breakGlass);
      setRowReingest(id, {
        kind: "summary",
        summary,
        startedAt,
        finishedAt: Date.now(),
      });
      clearRefusal(id);
      afterWrite();
    } catch (err: unknown) {
      setRowReingest(id, reingestRefusal(err));
    }
  };

  /** reingestAll is the fan-out. It issues one request per layer in
   * sequence, so a row shows it is running only while its own request is
   * open and no row shows progress the registry has not reported. What each
   * layer answered is collected into the run and reported once, because the
   * reader pressed one control: a report per layer stacked one dialog per
   * layer over the page, each naming a single layer and none stating the
   * run, and a refused layer sat behind that stack. */
  const reingestAll = async (): Promise<void> => {
    const targets = rows.map((row) => row.ID);
    setRun({ startedAt: Date.now(), outcomes: [], finishedAt: null });
    const outcomes: ReingestOutcome[] = [];
    for (const id of targets) {
      setRowReingest(id, { kind: "running" });
      try {
        const summary = await reingestLayer(id);
        outcomes.push({ layerID: id, kind: "summary", summary });
        clearRefusal(id);
      } catch (err: unknown) {
        outcomes.push({ layerID: id, kind: "refused", error: err });
      }
      setRowReingest(id, idleReingest);
    }
    setRun((prev) =>
      prev === null ? prev : { ...prev, outcomes, finishedAt: Date.now() },
    );
    afterWrite();
  };

  const commitMove = (from: string, onto: string) => {
    setDragging(null);
    setOver(null);
    const order = movedOrder(blockOf(rows, from), from, onto);
    if (order === null) {
      return;
    }
    const send = () => {
      reorderLayers(order).then(
        () => {
          clearRefusal(from);
          setOutcome(announced(movedNote(rows, order, from)));
          afterWrite();
        },
        (err: unknown) => {
          recordRefusal(from, err, send);
        },
      );
    };
    send();
  };

  /** moveBy walks one layer a step through its own class block. It is the
   * keyboard path to the reorder a drag commits, so the handle is a control a
   * keyboard-only operator can reach and the arrow keys drive the same
   * request the drop sends. */
  const moveBy = (id: string, delta: number) => {
    const block = blockOf(rows, id);
    const at = block.findIndex((row) => row.ID === id);
    if (at < 0) {
      return;
    }
    const onto = block[at + delta];
    if (onto === undefined) {
      setOutcome(announced(blockEdgeNote(block, id, delta)));
      return;
    }
    commitMove(id, onto.ID);
  };

  return (
    <section className="surface" aria-label="Layer panel">
      {/* The title, what a layer is, and the panel's actions share one row:
          the description states what the reader is looking at before the
          first row of the table asks them to infer it from the columns. */}
      <div className="panel-head">
        <div>
          <h1 ref={heading}>Layers</h1>
          <p className="lead">
            Sources the catalog is composed from. When two layers carry the same
            artifact ID, the higher precedence wins.
          </p>
        </div>
        <div className="panel-actions">
          {/* The recoverable link leads the row and states how much is still
            restorable, because that count is the one piece of panel state
            naming something on its way to being erased. The count is stated
            only where there is something to recover: a zero beside the link
            reads as a figure the operator has to act on, and the surface
            behind the link already states that nothing is recoverable. */}
          <a
            className="button link-action"
            data-testid="recoverable-link"
            href={deletedLayersHref}
            title={
              recoverable.error !== null
                ? "The recoverable count could not be read."
                : undefined
            }
          >
            ↺ Recently unregistered
            <RecoverableCount read={recoverable} />
          </a>
          <button
            type="button"
            className="button primary"
            disabled={readOnly}
            onClick={() => {
              setRegistering((open) => !open);
            }}
          >
            Register layer
          </button>
          <button
            type="button"
            disabled={readOnly || rows.length === 0 || reingesting}
            onClick={() => {
              void reingestAll();
            }}
          >
            Reingest all
          </button>
        </div>
      </div>
      {runReport}
      {/* §13.2.1 marks a read-only registry on its read responses, so the
          state is presented once here and every write control is unavailable
          at once rather than each one failing when it is pressed. */}
      {readOnly && (
        <Banner tone="danger">
          <span data-testid="read-only-banner">
            Something went wrong — the registry is temporarily read-only.
            Browsing and search still work.
          </span>
        </Banner>
      )}
      {registering && (
        <RegisterLayerForm
          subject={subject}
          knownGroups={grantedGroups(rows)}
          knownIDs={rows.map((row) => row.ID)}
          onRegistered={afterWrite}
          onClose={() => {
            setRegistering(false);
          }}
          readOnly={readOnly}
        />
      )}
      {/* The winning end of the order is named on the label itself. Left to
          the reader's inference from position, a table sorted the other way
          round reads the same. Both lines describe the reorder, so an empty
          panel drops them: instructions for moving rows that do not exist
          stand over the empty state as if the reader had missed something.
          A read-only registry takes no reorder and the handles are disabled
          for the pointer and the keyboard alike, so the label states that
          instead of an instruction that produces no response (§13.2.1). */}
      {rows.length > 0 && (
        <>
          <p className="precedence-label">
            <span className="label">
              {readOnly
                ? "Precedence — reordering is unavailable while the registry is read-only"
                : "Precedence — drag or press the arrow keys on a handle to reorder"}
            </span>
            <span className="quiet">lower row wins</span>
          </p>
          <p className="quiet">
            Every user-defined layer composes above every admin-defined layer,
            so a row moves within its own block.
          </p>
        </>
      )}
      {rows.length === 0 ? (
        <EmptyState title="No layers">
          Register a layer to bring its artifacts into the catalog.
        </EmptyState>
      ) : (
        // The table keeps its designed column widths down to a floor and
        // scrolls sideways inside its own container below that, so a narrow
        // viewport never squeezes a cell into one character to the line. The
        // container is focusable so a keyboard reaches the scroll it owns.
        <div className="table-scroll" tabIndex={0} role="region" aria-label="Layers">
          <table className="data-table layer-table">
            <thead>
              <tr>
                {/* The handle column carries no header. The control in it
                    names itself, and the design draws the header row as the
                    columns that name data (§13.10). */}
                <th className="drag-cell" />
                <th>
                  <span className="label">Layer</span>
                </th>
                <th>
                  <span className="label">Source</span>
                </th>
                <th>
                  <span className="label">Visibility</span>
                </th>
                <th>
                  <span className="label">Last ingest</span>
                </th>
                {/* The actions column carries no header, so the header row reads
                    as the columns that name data. */}
                <th />
              </tr>
            </thead>
            <tbody>
              {rows.map((layer, index) => (
                <LayerRow
                  key={layer.ID}
                  layer={layer}
                  position={index + 1}
                  subject={subject}
                  readOnly={readOnly}
                  refusal={refusals[layer.ID] ?? null}
                  reingest={reingest[layer.ID] ?? idleReingest}
                  reingestHeld={runActive}
                  dragging={dragging === layer.ID}
                  over={dropEdge(rows, dragging, over, layer.ID)}
                  onDragStart={() => {
                    setDragging(layer.ID);
                  }}
                  onDragOver={() => {
                    setOver(layer.ID);
                  }}
                  onDrop={() => {
                    if (dragging !== null) {
                      commitMove(dragging, layer.ID);
                    }
                  }}
                  onDragEnd={() => {
                    setDragging(null);
                    setOver(null);
                  }}
                  onMove={(delta) => {
                    moveBy(layer.ID, delta);
                  }}
                  onReingest={(breakGlass) => {
                    void runReingest(layer.ID, breakGlass);
                  }}
                  onDismissReingest={() => {
                    setRowReingest(layer.ID, idleReingest);
                  }}
                  onWrite={() => {
                    clearRefusal(layer.ID);
                    afterWrite();
                    // An unregister moves the layer into the recoverable list,
                    // so the count the header states is re-read on every write
                    // rather than only on the one that reopens the section.
                    recoverable.reload();
                  }}
                  onUnregistered={() => {
                    setOutcome(drawn(unregisteredNote(layer.ID)));
                    takeFocus(heading.current);
                  }}
                  onRefusal={(err, retry) => {
                    recordRefusal(layer.ID, err, retry);
                  }}
                  onDismissRefusal={() => {
                    clearRefusal(layer.ID);
                  }}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
      {/* The live region is rendered on every state of the panel, empty until
          a move lands. A region mounted at the moment its text arrives is not
          in the accessibility tree when the change happens, and the
          announcement is dropped. */}
      <p
        className={outcome.visible ? "banner banner-accent" : "assistive-only"}
        role="status"
        aria-live="polite"
        data-testid="panel-announcement"
      >
        {outcome.text}
      </p>
      <PanelFoot rows={rows} subject={subject} readOnly={readOnly} />
    </section>
  );
}

/** PanelFoot closes the panel with the two facts no row carries: how many
 * layers of the caller's own the §7.3.1 cap still leaves them, and that a
 * reorder rewrites composition order alone. The cap comes from the §4.7.8
 * quota read, which the registry gates on no role and the account menu takes
 * against the same endpoint.
 *
 * The denominator is stated only where that read reports a positive cap. The
 * value zero selects the deployment default and no response reports what that
 * default resolved to, and a read that fails reports nothing, so both arms
 * state the count alone rather than a limit no response carried. A caller who
 * resolves no subject owns no row the panel can recognize as theirs, so that
 * arm carries the reordering note by itself.
 *
 * The reordering note describes moving a row, so an empty panel drops it for
 * the same reason the precedence lines above the table are dropped there, and
 * a read-only registry drops it as well: it states when a reorder takes
 * effect, and on that posture no reorder is taken (§13.2.1). A caller who
 * resolves no subject and holds no row is left with nothing to state, and the
 * foot is absent rather than blank. */
function PanelFoot({
  rows,
  subject,
  readOnly,
}: {
  rows: LayerRecord[];
  subject: string;
  readOnly: boolean;
}) {
  const quota = useAsync(() => readQuota(), []);
  const cap = quota.value?.limits?.MaxUserLayers;
  const mine = rows.filter((row) => ownedByCaller(row, subject)).length;
  const holding = subject !== "";
  const reorderable = rows.length > 0 && !readOnly;
  if (!holding && !reorderable) {
    return null;
  }
  return (
    <p className="panel-foot quiet">
      {holding && (
        <span data-testid="personal-layer-count">
          {personalHolding(mine, cap)}
        </span>
      )}
      {holding && reorderable && (
        <span className="foot-divider" aria-hidden="true" />
      )}
      {reorderable && (
        <span>
          Reordering takes effect on the next read; it does not trigger a
          reingest.
        </span>
      )}
    </p>
  );
}

/** personalHolding states how many user-defined layers the caller holds,
 * against the cap where the quota read reported one. */
function personalHolding(mine: number, cap: number | undefined): string {
  if (cap !== undefined && cap > 0) {
    return `You have ${String(mine)} of ${String(cap)} personal layers.`;
  }
  return `You have ${String(mine)} personal ${mine === 1 ? "layer" : "layers"}.`;
}

/** listOrdered reads the layer list in the order §4.6 composes it in. The
 * order value sets precedence within a class, and the composition rule places
 * every user-defined layer above every admin-defined layer whatever the stored
 * order values are, so the rows are grouped by class first and sorted by order
 * inside each group. Sorting the whole list by order alone would name the
 * wrong winning row on any tenant whose most recently registered layer is
 * admin-defined, because registration hands each new layer the highest
 * existing order. */
async function listOrdered(): Promise<LayerRecord[]> {
  const layers = await listLayers();
  const byOrder = (a: LayerRecord, b: LayerRecord) => a.Order - b.Order;
  const admin = layers
    .filter((layer) => layer.UserDefined !== true)
    .sort(byOrder);
  const user = layers
    .filter((layer) => layer.UserDefined === true)
    .sort(byOrder);
  return [...admin, ...user];
}

/** blockOf returns the contiguous run of rows a layer shares its class with,
 * which is the run a reorder may move it inside. A move across the class
 * boundary changes no composition order, because §4.6 composes every
 * user-defined layer above every admin-defined one whatever the stored order
 * values are, so the block bounds both where the control can take a row and
 * what the request names. */
function blockOf(rows: LayerRecord[], id: string): LayerRecord[] {
  const moving = rows.find((row) => row.ID === id);
  if (moving === undefined) {
    return [];
  }
  return rows.filter(
    (row) => (row.UserDefined === true) === (moving.UserDefined === true),
  );
}

/** movedOrder returns the block's resulting order after the dragged row is
 * dropped onto another row of the same class, or null where the drop names no
 * move the block can make.
 *
 * The reorder endpoint assigns each layer the request names an absolute order
 * value taken from its position in the request rather than swapping two
 * stored values. A request naming the moved pair alone therefore stamps the
 * block's first order values onto that pair and leaves every other row of the
 * block holding the value it already had, which ties or inverts rows the move
 * was not meant to touch. The request names the whole block so the endpoint's
 * positional assignment reproduces the order the panel displayed.
 *
 * Every layer the request names is authorized on its own under the §7.3.1
 * layer-write rule, and the list read is unfiltered, so a block holding a
 * layer this caller may not write has its move refused whole. The panel
 * presents that refusal on the row rather than predicting it. */
function movedOrder(
  block: LayerRecord[],
  from: string,
  onto: string,
): string[] | null {
  const order = block.map((row) => row.ID);
  const at = order.indexOf(from);
  const target = order.indexOf(onto);
  if (at < 0 || target < 0 || at === target) {
    return null;
  }
  order.splice(at, 1);
  order.splice(target, 0, from);
  return order;
}

/** DropEdge is the edge of the row under the pointer that the insertion
 * indicator is drawn on, or null where the row is no drop target. */
type DropEdge = "above" | "below" | null;

/** dropEdge names which edge of the target row the drop would insert on. The
 * dragged row takes the target's slot, so a row moving up the table lands
 * above the row it is dropped onto and a row moving down lands below it. An
 * indicator fixed to the top edge therefore marks the slot above the target
 * on a downward drag, which is one place higher than the row will land. */
function dropEdge(
  rows: LayerRecord[],
  dragging: string | null,
  over: string | null,
  id: string,
): DropEdge {
  if (dragging === null || over !== id || dragging === id) {
    return null;
  }
  const at = rows.findIndex((row) => row.ID === dragging);
  const target = rows.findIndex((row) => row.ID === id);
  if (at < 0 || target < 0) {
    return null;
  }
  return target > at ? "below" : "above";
}

/** movedNote states where a committed reorder left the layer, in the same
 * terms the row itself carries: the position counted down the whole table,
 * which is the precedence order the panel is about. The moved block is the
 * contiguous run of one class, so the block's first row holds the offset the
 * new in-block index is counted from. */
function movedNote(
  rows: LayerRecord[],
  order: string[],
  id: string,
): string {
  const offset = rows.findIndex((row) => order.includes(row.ID));
  const at = order.indexOf(id);
  const position = offset + at + 1;
  return `${id} moved to order ${String(position)} of ${String(rows.length)}.`;
}

/** blockEdgeNote states why an arrow key moved nothing. A row at either end
 * of its class block has nowhere to step, because §4.6 composes every
 * user-defined layer above every admin-defined one whatever the stored order
 * values are, so a step across the class boundary names no move. The refusal
 * goes to the live region a committed move states its outcome in: a reader
 * who cannot see the rows stay put otherwise hears the previous move's
 * confirmation or nothing at all. */
function blockEdgeNote(
  block: LayerRecord[],
  id: string,
  delta: number,
): string {
  const klass =
    block[0]?.UserDefined === true ? "user-defined" : "admin-defined";
  const edge = delta < 0 ? "first" : "last";
  return `${id} is already ${edge} among the ${klass} layers; it did not move.`;
}

/** unregisteredNote states what a committed unregister did and where the
 * layer went, because the row that carried it is gone from the table by the
 * time the reader hears anything. The retention window is named where the
 * confirmation named it, so the two statements of it do not drift. */
function unregisteredNote(id: string): string {
  return `${id} is unregistered. It is restorable from Recently unregistered until ${erasesOn(new Date())}.`;
}

function LayerRow({
  layer,
  position,
  subject,
  readOnly,
  refusal,
  reingest,
  reingestHeld,
  dragging,
  over,
  onDragStart,
  onDragOver,
  onDrop,
  onDragEnd,
  onMove,
  onReingest,
  onDismissReingest,
  onWrite,
  onUnregistered,
  onRefusal,
  onDismissRefusal,
}: {
  layer: LayerRecord;
  position: number;
  subject: string;
  readOnly: boolean;
  refusal: Refusal | null;
  reingest: ReingestState;
  /** reingestHeld is the panel's fan-out running over every layer, this one
   * included. The row's trigger is held for as long as it runs, so one layer
   * is never reingested twice at once. */
  reingestHeld: boolean;
  dragging: boolean;
  /** over is the edge the drop would insert on, or null where this row is
   * not the row under the pointer. */
  over: DropEdge;
  onDragStart: () => void;
  onDragOver: () => void;
  onDrop: () => void;
  onDragEnd: () => void;
  onMove: (delta: number) => void;
  onReingest: (breakGlass?: BreakGlass) => void;
  onDismissReingest: () => void;
  onWrite: () => void;
  /** onUnregistered reports the one write that takes this row away with it,
   * so the panel can state what happened and take the focus the row's own
   * controls leave behind. */
  onUnregistered: () => void;
  onRefusal: (err: unknown, retry: () => void) => void;
  onDismissRefusal: () => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const [editing, setEditing] = useState(false);
  const [overflowOpen, setOverflowOpen] = useState(false);
  // The Reingest button is the row's stable control, and it is where focus
  // returns from every row state that takes away the control focus was on:
  // the button disables itself while its request is open, which blurs it,
  // and a refusal banner's Dismiss goes away with the banner it closes. The
  // browser leaves focus on the document body in both cases, which puts the
  // reader back at the top of the page.
  const trigger = useRef<HTMLButtonElement>(null);
  // Focus is owed only from a press on this row's own controls. The panel's
  // fan-out drives every row at once, and a row claiming focus for a request
  // the reader started from the panel's control moves them into the table.
  const owed = useRef(false);
  const oweFocus = () => {
    owed.current = true;
  };
  const refused = refusal !== null;
  useEffect(() => {
    // A request still open keeps the debt: the trigger is disabled for as
    // long as it runs, and focusing a disabled control does nothing.
    if (!owed.current || reingest.kind === "running") {
      return;
    }
    // A dialog the state opened holds focus for itself, and it hands focus on
    // when it closes, which is the render this runs on again.
    const held = document.activeElement;
    if (held !== null && held !== document.body) {
      return;
    }
    owed.current = false;
    trigger.current?.focus();
  }, [reingest.kind, refused]);
  // Picking an item unmounts the menu, so the trigger takes focus back before
  // the dialog the item opens reads what to return focus to.
  const overflow = useRef<HTMLButtonElement>(null);
  // The menu is a transient popup, so it dismisses on Escape and on a press
  // or a focus move outside itself, which is also what keeps one row's
  // actions from staying open behind another row's.
  const menu = usePopupDismiss<HTMLDivElement>(
    overflowOpen,
    () => {
      setOverflowOpen(false);
    },
    overflow,
  );

  // A write the panel sends can come back refused, including on a row the
  // panel presented as this caller's to manage. The refusal is drawn on the
  // row on the envelope's own terms: the code, the message, and the
  // remediation the registry names. It reports neither who owns the layer nor
  // the state of the session, because the refusal carries neither.
  // The refusal carries the write beside it, so Try again re-issues exactly
  // the action that was refused rather than a fresh guess at it.
  const attempt = (run: () => Promise<unknown>, done?: () => void) => {
    run().then(
      () => {
        onWrite();
        done?.();
      },
      (err: unknown) => {
        onRefusal(err, () => {
          attempt(run, done);
        });
      },
    );
  };

  const rowClass = [
    dragging ? "row-dragging" : "",
    over === null ? "" : `row-drop-${over}`,
  ].filter((name) => name !== "");

  // A refusal is full-width prose: a code, the envelope's own message, its
  // remediation, and the controls that clear it. The actions column is a
  // fixed narrow column in a grid every row shares, so a card drawn inside it
  // stretched the row to the height of the card, left the other cells empty
  // over that height, and pushed the row's own controls apart. The card is
  // drawn in a full-width row directly under the layer's own row instead,
  // which keeps it on the row it belongs to and leaves every cell and every
  // control where the reader left them.
  const detail = reingest.kind !== "idle" || refusal !== null;

  return (
    <>
    <tr
      className={rowClass.join(" ")}
      // The row is a drop target rather than a drag source: `draggable` sits
      // on the handle alone, which is what the panel's instruction and the
      // handle column promise, and it is the whole of the row a pointer can
      // pick up. A draggable row started a precedence reorder from a drag
      // begun anywhere in it, and it took the row's text out of the
      // selection model, so the source path could not be selected with the
      // mouse. The dragstart the handle fires bubbles here, where the drag
      // and its drop are handled together.
      onDragStart={(event) => {
        // The drag data store is what makes the drag a drag: a dragstart
        // that leaves it empty is a cancelled drag under the HTML model, so
        // a browser that enforces that fires neither dragover nor drop and
        // the pointer reorder is lost with no sign of it. The row carries
        // its own layer ID, and the move it names is a reorder rather than
        // a copy.
        event.dataTransfer.setData("text/plain", layer.ID);
        event.dataTransfer.effectAllowed = "move";
        onDragStart();
      }}
      onDragOver={(event) => {
        // A row that does not cancel the drag-over event is not a drop
        // target, so the drop never fires and the move is silently lost.
        event.preventDefault();
        onDragOver();
      }}
      onDrop={(event) => {
        event.preventDefault();
        onDrop();
      }}
      onDragEnd={onDragEnd}
    >
      <td className="drag-cell">
        {/* The handle is a button so the reorder has an input path that is
            not a pointer drag. A keyboard-only operator focuses it and the
            arrow keys move the row through its block, which sends the same
            request a drop sends. */}
        <button
          type="button"
          className="drag-handle"
          aria-label={`Move ${layer.ID}: press the up or down arrow key`}
          disabled={readOnly}
          draggable={!readOnly}
          onKeyDown={(event) => {
            if (event.key !== "ArrowUp" && event.key !== "ArrowDown") {
              return;
            }
            // The arrows scroll the page otherwise, which moves the row out
            // from under the operator driving it.
            event.preventDefault();
            onMove(event.key === "ArrowUp" ? -1 : 1);
          }}
        >
          ⋮⋮
        </button>
      </td>
      <td className="mono">
        {/* The name and the marker qualifying it are one wrapping row, so the
            gap between them comes from the row rather than from a badge's own
            margin, which is trailing only. The badge beside the name carries
            the ownership marker alone; the row's place in the precedence order
            is the fact the panel is about, so it sits on its own line under
            the name where every row states it at the same offset. */}
        <div className="layer-id-cell">
          <span className="layer-name">{layer.ID}</span>
          {ownedByCaller(layer, subject) && <Badge tone="accent">yours</Badge>}
        </div>
        <div className="layer-order quiet" data-testid="layer-order">
          {orderNote(position, layer)}
        </div>
      </td>
      <td className="source-col">
        <SourceCell layer={layer} />
      </td>
      <td>
        <VisibilityCell layer={layer} />
      </td>
      <td className="mono">
        <LastIngestCell layer={layer} />
      </td>
      <td className="row-actions">
        {/* The actions column is fixed width and every row shares one grid,
            so the bar carries the one action a reader reaches for on a row,
            and the rest sit behind the overflow control. Stacking all three
            controls tripled the height of every row. */}
        <div className="row-action-bar">
          <ReingestButton
            state={reingest}
            readOnly={readOnly}
            held={reingestHeld}
            buttonRef={trigger}
            onStart={(breakGlass) => {
              oweFocus();
              onReingest(breakGlass);
            }}
          />
          <button
            ref={overflow}
            type="button"
            className="row-overflow"
            aria-label={`More actions for ${layer.ID}`}
            aria-haspopup="menu"
            aria-expanded={overflowOpen}
            onClick={() => {
              setOverflowOpen((open) => !open);
            }}
          >
            ⋯
          </button>
        </div>
        {overflowOpen && (
          <RowMenu
            menuRef={menu}
            anchor={overflow}
            label={`More actions for ${layer.ID}`}
            items={[
              {
                label: "Edit",
                disabled: readOnly,
                onSelect: () => {
                  overflow.current?.focus();
                  setOverflowOpen(false);
                  setEditing((open) => !open);
                },
              },
              {
                label: "Unregister",
                disabled: readOnly,
                onSelect: () => {
                  overflow.current?.focus();
                  setOverflowOpen(false);
                  setConfirming(true);
                },
              },
            ]}
          />
        )}
        {editing && (
          <UpdateLayerForm
            layer={layer}
            readOnly={readOnly}
            onUpdated={onWrite}
            onClose={() => {
              setEditing(false);
            }}
          />
        )}
        {confirming && (
          <UnregisterConfirmation
            layer={layer}
            onCancel={() => {
              setConfirming(false);
            }}
            onConfirm={() => {
              setConfirming(false);
              attempt(() => unregisterLayer(layer.ID), onUnregistered);
            }}
          />
        )}
      </td>
    </tr>
    {detail && (
      <tr className="row-detail">
        <td colSpan={6}>
          <ReingestStatus
            layerID={layer.ID}
            state={reingest}
            onStart={(breakGlass) => {
              oweFocus();
              onReingest(breakGlass);
            }}
            onDismiss={() => {
              oweFocus();
              onDismissReingest();
            }}
          />
          {refusal !== null && (
            /* The refusal is cleared by re-issuing the write or by dismissing
               it. Every other control on the row stays live. ErrorState draws
               the envelope's message and its remediation beside the code, and
               it withholds Try again where the envelope reports the condition
               does not clear on its own: a browser-origin refusal answers an
               identical re-issue identically, so the recovery on offer is the
               one the registry names rather than a loop. */
            <ErrorState
              error={refusal.error}
              title="The registry refused that action and nothing changed."
              onRetry={refusal.retry}
            >
              <button
                type="button"
                onClick={() => {
                  oweFocus();
                  onDismissRefusal();
                }}
              >
                Dismiss
              </button>
            </ErrorState>
          )}
        </td>
      </tr>
    )}
    </>
  );
}

/** useAnchoredPlacement returns the viewport coordinates that put a popup
 * directly under its trigger and right-aligned with it. The popup is drawn
 * into the document rather than into the row it belongs to, so it carries the
 * position its trigger has rather than one the table's own layout gives it,
 * and it is placed again on a scroll or a resize because the trigger moves
 * under both. */
function useAnchoredPlacement(anchor: RefObject<HTMLElement | null>) {
  const [placement, setPlacement] = useState<{ top: number; right: number }>({
    top: 0,
    right: 0,
  });
  useLayoutEffect(() => {
    const place = () => {
      const rect = anchor.current?.getBoundingClientRect();
      if (rect === undefined) {
        return;
      }
      setPlacement({
        top: rect.bottom + 6,
        right: window.innerWidth - rect.right,
      });
    };
    place();
    // Capture, so the table's own sideways scroll moves the popup with the
    // trigger rather than leaving it over the column the trigger has left.
    window.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
    return () => {
      window.removeEventListener("scroll", place, true);
      window.removeEventListener("resize", place);
    };
  }, [anchor]);
  return placement;
}

/** RowMenu is the popup behind a row's overflow control. It carries menu
 * semantics, so an assistive technology announces it as a menu and its
 * entries as menu items, and the label the popup states is read rather than
 * dropped, which is what a bare div does with an aria-label.
 *
 * The keyboard treatment is the roving tabindex the artifact viewer's tab set
 * already uses: the menu is one Tab stop, it opens with focus on its first
 * item, and the arrow keys move between items. Leaving focus on the trigger
 * made a forward Tab the only route into a popup the reader had just opened,
 * and past the last item on the row after that.
 *
 * The menu overlays the table rather than taking space in it. Drawn in the
 * flow of the row's fixed-width actions cell it stretched that row to the
 * height of the menu, emptied every other cell in the row over that height,
 * and pushed every row below it down the page, so a reader who opened a menu
 * lost the row they were reading. It is drawn into the document instead,
 * positioned against its trigger, because the table scrolls sideways inside a
 * container that clips what overflows it and a menu placed against the cell
 * was cut off at the bottom of the table. */
function RowMenu({
  menuRef,
  anchor,
  label,
  items,
}: {
  menuRef: RefObject<HTMLDivElement | null>;
  anchor: RefObject<HTMLElement | null>;
  label: string;
  items: { label: string; disabled: boolean; onSelect: () => void }[];
}) {
  const [active, setActive] = useState(0);
  const placement = useAnchoredPlacement(anchor);
  const focusItem = (container: HTMLElement | null, index: number) => {
    container
      ?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')
      [index]?.focus();
  };
  useEffect(() => {
    focusItem(menuRef.current, 0);
  }, [menuRef]);

  const onArrow = (event: KeyboardEvent<HTMLDivElement>) => {
    let next = active;
    switch (event.key) {
      case "ArrowDown":
        next = (active + 1) % items.length;
        break;
      case "ArrowUp":
        next = (active - 1 + items.length) % items.length;
        break;
      case "Home":
        next = 0;
        break;
      case "End":
        next = items.length - 1;
        break;
      default:
        return;
    }
    // The arrows scroll the page otherwise, which moves the menu out from
    // under the reader driving it.
    event.preventDefault();
    setActive(next);
    focusItem(event.currentTarget, next);
  };

  return createPortal(
    <div
      ref={menuRef}
      className="row-menu"
      role="menu"
      aria-label={label}
      style={placement}
      onKeyDown={onArrow}
    >
      {items.map((item, index) => (
        <button
          key={item.label}
          type="button"
          role="menuitem"
          // The roving tabindex: the menu is one Tab stop, and the item the
          // arrows last moved to is the one it lands on.
          tabIndex={index === active ? 0 : -1}
          disabled={item.disabled}
          onClick={item.onSelect}
        >
          {item.label}
        </button>
      ))}
    </div>,
    document.body,
  );
}

/**
 * UnregisterConfirmation gates the one write whose effect reaches callers
 * who never touched this panel. It states both halves of what unregistering
 * does: the layer's artifacts leave every caller's view at the next sync,
 * and the layer stays restorable until the recovery window runs out. The
 * write is issued only once the reader has typed the layer's own ID, so the
 * action cannot be taken by a single press on the row it sits in.
 *
 * It is a dialog over a scrim rather than a panel inside the row's actions
 * cell. Rendered into the cell it took the column's width, so the statements
 * wrapped to a few words a line and ran past the edge of the table, and it
 * grew the row it opened from by several hundred pixels, which pushed every
 * row below it down the page while the reader was deciding.
 */
function UnregisterConfirmation({
  layer,
  onCancel,
  onConfirm,
}: {
  layer: LayerRecord;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const [typed, setTyped] = useState("");
  // A held write names what is holding it, the way the register form's footer
  // does. Without the sentence the reader is left pressing a disabled control
  // that reports no reason, and a reader who has scrolled past the field or is
  // hearing the button announced has nothing to go on at all.
  const held = typed !== layer.ID;
  // The field is empty on the first paint, so the hold stands before the
  // reader has done anything. Stating it then would open the confirmation on a
  // sentence in the refusal colour, reading as an error the reader has already
  // caused; the field's own label carries the instruction until they have
  // typed. The sentence is stated once what they typed does not match.
  const stated = held && typed !== "";
  const holdID = useId();
  return (
    <Modal title={`Unregister ${layer.ID}`} onClose={onCancel}>
      <div className="modal-body">
        {/* The reach of the write leads, in the danger tone, because it is
            the half that cannot be undone by the reader alone. */}
        <Banner tone="danger" glyph="!">
          <p className="banner-title">
            Its artifacts disappear from every caller&rsquo;s view.
          </p>
          <p>They leave the catalog the next time each caller syncs.</p>
        </Banner>
        {/* The recovery window is the half that limits the damage, so it is
            stated in the neutral tone beside it rather than buried under it. */}
        <Banner glyph="↺">
          <p className="banner-title">Recoverable for {recoveryDays} days.</p>
          <p>
            The layer and its artifacts are kept and can be restored from
            Recently unregistered until {erasesOn(new Date())}, after which it
            is erased.
          </p>
        </Banner>
        {/* What the layer grants today, so the reader confirms against the
            audience the write takes it from rather than against its ID alone. */}
        {/* One labelled value takes the borderless list the rail's provenance
            and the resource detail already use, rather than a bordered table.
            A `th` is drawn bold and in the body face by the user agent, so a
            single-row table set the word "visibility" heavier than the grants
            it labels and read as a table header over the dialog rather than
            as a key beside its value (§13.10). */}
        <dl className="rail-facts" data-testid="unregister-properties">
          <div className="rail-fact">
            <dt className="mono">visibility</dt>
            <dd>{visibilitySummary(layer)}</dd>
          </div>
        </dl>
        <label className="field">
          <span className="label">Type the layer ID to confirm</span>
          <input
            type="text"
            value={typed}
            // The hold is stated in the footer, and a reader working in the
            // field never reaches that line, so the field points at it too.
            aria-describedby={stated ? holdID : undefined}
            onChange={(event) => {
              setTyped(event.target.value);
            }}
            // A single-field entry control takes Enter as its commit, the same
            // way the version picker does, because a reader who has typed the
            // ID reaches for the return key before the adjacent button. Enter
            // commits only on the match the button gates on, so it cannot
            // issue the write from a half-typed ID.
            onKeyDown={(event) => {
              if (event.key === "Enter" && typed === layer.ID) {
                onConfirm();
              }
            }}
          />
        </label>
      </div>
      {/* Cancel leads the footer and the destructive control carries the
          danger tone, so the press that reaches every caller is the one the
          reader has to aim for. */}
      <div className="modal-foot">
        {stated && (
          <span
            className="modal-foot-note modal-foot-hold"
            id={holdID}
            data-testid="unregister-foot-note"
          >
            Type the layer ID to confirm the unregistration.
          </span>
        )}
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
        <button
          type="button"
          className="button danger"
          disabled={held}
          aria-describedby={stated ? holdID : undefined}
          onClick={onConfirm}
        >
          Unregister layer
        </button>
      </div>
    </Modal>
  );
}

/** orderNote states the row's place in the precedence order, with the layer's
 * stored owner appended where the layer carries one.
 *
 * The position counts down the table rather than reading the stored `Order`
 * field, because that field sets precedence within a class alone: §4.6
 * composes every user-defined layer above every admin-defined layer whatever
 * the stored values are, and registration hands each new layer the highest
 * existing order, so the stored values are neither contiguous nor comparable
 * across the two blocks. The table is already sorted the way the catalog
 * composes, so its own positions are the precedence the panel is about.
 *
 * The owner is stated as the field it is, with no ownership language: on an
 * admin-defined layer it is supplied by the caller who registered or patched
 * the layer and names no authorized subject, and the ownership marker beside
 * the name is what asserts who may write. */
function orderNote(position: number, layer: LayerRecord): string {
  const note = `order ${String(position)}`;
  if (layer.Owner === undefined || layer.Owner === "") {
    return note;
  }
  return `${note} · owner ${layer.Owner}`;
}

/** ownedByCaller is the panel's ownership marker. It is a property of a
 * user-defined row alone: on such a row it compares the row's stored owner
 * against the caller's own subject, and the posture read reports a subject
 * only where one resolves, so a caller with no subject carries no marker on
 * any row. An admin-defined row carries no marker on any value of its stored
 * owner, because the write rule authorizes a tenant admin alone there and
 * that owner names no authorized subject. */
function ownedByCaller(layer: LayerRecord, subject: string): boolean {
  return (
    layer.UserDefined === true && subject !== "" && layer.Owner === subject
  );
}

/** LastIngestCell states when the layer was last ingested as an age, the way
 * the sidebar footer states the same fact, with the ingest reference the run
 * landed on beneath it. The stored stamp is a microsecond ISO-8601 string
 * that wraps over two lines in a column this narrow and reads as a value to
 * decode rather than a fact to scan, so the exact stamp moves to the cell's
 * title and the age is what the row displays. */
function LastIngestCell({ layer }: { layer: LayerRecord }) {
  const at = layer.last_ingested_at ?? "";
  const ref = ingestRef(layer);
  return (
    <div title={at === "" ? undefined : at}>
      <div>{at === "" ? "never" : since(at, Date.now())}</div>
      {/* An em dash rather than an empty line: a row that displays no
          reference keeps the same height as one that does. */}
      <div className="quiet ingest-ref">{ref === "" ? "—" : shortRef(ref)}</div>
    </div>
  );
}

/** VisibilityCell renders one marker per matching axis, in the fixed order
 * public, organization, groups, then users, because §4.6 defines visibility
 * as independent grants that combine as a union. Two layers carrying the same
 * grants therefore read identically, and no axis is dropped. */
function VisibilityCell({ layer }: { layer: LayerRecord }) {
  const markers = visibilityMarkers(layer);
  if (markers.length === 0) {
    // The absent grant is stated as a marker rather than as body text, and in
    // the same wrapping cell a granted row uses, so the column keeps one row
    // of markers whatever the grant state.
    return (
      <div className="visibility-markers">
        <Badge tone="hollow">no grants — only you</Badge>
      </div>
    );
  }
  // The markers share one wrapping cell so a row that grants on four axes
  // keeps the height of a row that grants on one. Left to the badge's own
  // inline flow, a marker naming members broke over its own words and every
  // axis took a line of its own, which tripled the row.
  return (
    <div className="visibility-markers">
      {markers.map((marker) => (
        <Badge key={marker.named} tone="grant">
          {/* The names are the half the cell may clip, and the remainder
              count is the half it may not: a marker cut off mid-list without
              its count states neither who is granted nor how many are. */}
          <span className="marker-named">{marker.named}</span>
          {marker.extra > 0 ? (
            <span className="marker-extra">{` +${String(marker.extra)}`}</span>
          ) : null}
        </Badge>
      ))}
    </div>
  );
}

