import type * as React from "react";

/*
 * The landing page's inline SVG artwork: the six feature diagrams, the three
 * deployment icons, and the six server-side integration icons.
 *
 * The geometry is transcribed from site/design/podium-design-reference.html.
 * The reference carries one copy per theme with hard-coded fills; this module
 * carries one copy whose colours come from classes defined in landing.css, so
 * a single set of markup serves both themes:
 *
 *   .l-dg-ink / .l-dg-ink-fill      primary shapes
 *   .l-dg-meta / .l-dg-meta-fill    connectors and de-emphasized shapes
 *   .l-dg-accent / .l-dg-accent-fill  selected and visible states
 *   .l-dg-tick                      a glyph drawn on top of an accent fill
 *   .l-dg-paper                     the panel colour behind a primary shape
 *
 * Every diagram is decorative and carries aria-hidden, because the card text
 * beside it states the same thing in prose.
 */

/** Names the diagram a feature card shows. */
export type FeatureDiagramKey =
  | "delivery"
  | "domains"
  | "materialization"
  | "discovery"
  | "layers"
  | "access";

/** Names the icon a deployment card shows. */
export type DeploymentIconKey = "folder" | "database" | "asterisk";

/** Names the icon an integration row shows. */
export type IntegrationIconKey =
  | "cylinder"
  | "cube"
  | "nodes"
  | "sparkle"
  | "padlock"
  | "branch";

/* --------------------------------------------------------- feature diagrams */

/** One canonical artifact fanning out into three harness-specific formats. */
export function DeliveryDiagram(): React.ReactElement {
  return (
    <svg className="l-dg" width="200" height="80" viewBox="0 0 240 96" aria-hidden="true">
      <rect
        className="l-dg-paper l-dg-ink"
        x="4"
        y="30"
        width="44"
        height="36"
        rx="4"
        strokeWidth="1.8"
      />
      <rect className="l-dg-ink-fill" x="12" y="41" width="28" height="3" rx="1.5" opacity=".5" />
      <rect className="l-dg-ink-fill" x="12" y="49" width="20" height="3" rx="1.5" opacity=".5" />
      <g className="l-dg-meta" fill="none" strokeWidth="1.8">
        <path d="M48 48h22c8 0 8-30 16-30h14" />
        <path d="M48 48h52" />
        <path d="M48 48h22c8 0 8 30 16 30h14" />
      </g>
      <g className="l-dg-meta" fill="none" strokeWidth="1.8" strokeLinejoin="round">
        <path d="M94 14l6 4-6 4" />
        <path d="M94 44l6 4-6 4" />
        <path d="M94 74l6 4-6 4" />
      </g>
      <g className="l-dg-mono l-dg-ink-fill" fontSize="10">
        <rect
          className="l-dg-ink"
          x="104"
          y="7"
          width="96"
          height="22"
          rx="4"
          fill="none"
          strokeWidth="1.6"
        />
        <circle className="l-dg-accent-fill" cx="115" cy="18" r="4.5" />
        <text x="126" y="22">
          Claude Code
        </text>
        <rect
          className="l-dg-ink"
          x="104"
          y="37"
          width="96"
          height="22"
          rx="4"
          fill="none"
          strokeWidth="1.6"
        />
        <rect className="l-dg-accent-fill" x="110.5" y="43.5" width="9" height="9" />
        <text x="126" y="52">
          Cursor
        </text>
        <rect
          className="l-dg-ink"
          x="104"
          y="67"
          width="96"
          height="22"
          rx="4"
          fill="none"
          strokeWidth="1.6"
        />
        <path className="l-dg-accent-fill" d="M115 73l5 9h-10z" />
        <text x="126" y="82">
          Codex
        </text>
      </g>
    </svg>
  );
}

/** A catalog whose folders nest into domains and subdomains. */
export function DomainsDiagram(): React.ReactElement {
  return (
    <svg className="l-dg" width="200" height="80" viewBox="0 0 240 96" aria-hidden="true">
      <g className="l-dg-mono" fontSize="10">
        <rect
          className="l-dg-ink"
          x="4"
          y="2"
          width="66"
          height="16"
          rx="3"
          fill="none"
          strokeWidth="1.4"
        />
        <text className="l-dg-ink-fill" x="12" y="13.5">
          catalog
        </text>
        <g className="l-dg-meta" strokeWidth="1.4" fill="none">
          <path d="M14 18v9h10" />
          <path d="M14 18v50h10" />
          <path d="M14 18v69h10" />
        </g>
        <rect
          className="l-dg-ink"
          x="24"
          y="21"
          width="74"
          height="16"
          rx="3"
          fill="none"
          strokeWidth="1.4"
        />
        <text className="l-dg-ink-fill" x="32" y="32.5">
          Platform
        </text>
        <g className="l-dg-meta" strokeWidth="1.4" fill="none">
          <path d="M34 37v9h10" />
        </g>
        <rect
          className="l-dg-ink"
          x="44"
          y="40"
          width="34"
          height="16"
          rx="3"
          fill="none"
          strokeWidth="1.4"
        />
        <text className="l-dg-ink-fill" x="52" y="51.5">
          ci
        </text>
        <rect
          className="l-dg-ink"
          x="24"
          y="60"
          width="78"
          height="16"
          rx="3"
          fill="none"
          strokeWidth="1.4"
        />
        <text className="l-dg-ink-fill" x="32" y="71.5">
          Analytics
        </text>
        <rect
          className="l-dg-ink"
          x="24"
          y="79"
          width="70"
          height="16"
          rx="3"
          fill="none"
          strokeWidth="1.4"
        />
        <text className="l-dg-ink-fill" x="32" y="90.5">
          Finance
        </text>
      </g>
    </svg>
  );
}

/** Two artifacts picked out of a catalog and materialized into a workspace. */
export function MaterializationDiagram(): React.ReactElement {
  return (
    <svg className="l-dg" width="200" height="80" viewBox="0 0 240 96" aria-hidden="true">
      <g className="l-dg-meta" strokeWidth="1.6" strokeDasharray="5 4" fill="none">
        <rect x="4" y="8" width="96" height="80" rx="5" />
      </g>
      <rect
        className="l-dg-meta"
        x="12"
        y="14"
        width="38"
        height="14"
        rx="3"
        fill="none"
        strokeWidth="1.4"
        opacity="0.55"
      />
      <rect
        className="l-dg-accent-fill l-dg-accent"
        x="56"
        y="14"
        width="38"
        height="14"
        rx="3"
        strokeWidth="1.4"
        opacity="1"
      />
      <path className="l-dg-tick" d="M62 23l3 3 6-6" strokeWidth="1.8" fill="none" />
      <rect
        className="l-dg-meta"
        x="12"
        y="32"
        width="38"
        height="14"
        rx="3"
        fill="none"
        strokeWidth="1.4"
        opacity="0.55"
      />
      <rect
        className="l-dg-meta"
        x="56"
        y="32"
        width="38"
        height="14"
        rx="3"
        fill="none"
        strokeWidth="1.4"
        opacity="0.55"
      />
      <rect
        className="l-dg-accent-fill l-dg-accent"
        x="12"
        y="50"
        width="38"
        height="14"
        rx="3"
        strokeWidth="1.4"
        opacity="1"
      />
      <path className="l-dg-tick" d="M18 59l3 3 6-6" strokeWidth="1.8" fill="none" />
      <rect
        className="l-dg-meta"
        x="56"
        y="50"
        width="38"
        height="14"
        rx="3"
        fill="none"
        strokeWidth="1.4"
        opacity="0.55"
      />
      <rect
        className="l-dg-meta"
        x="12"
        y="68"
        width="38"
        height="14"
        rx="3"
        fill="none"
        strokeWidth="1.4"
        opacity="0.55"
      />
      <rect
        className="l-dg-meta"
        x="56"
        y="68"
        width="38"
        height="14"
        rx="3"
        fill="none"
        strokeWidth="1.4"
        opacity="0.55"
      />
      <path className="l-dg-meta" d="M108 48h16m-5-4l5 4-5 4" strokeWidth="1.6" fill="none" />
      <rect className="l-dg-accent-fill" x="136" y="31" width="38" height="14" rx="3" />
      <rect className="l-dg-accent-fill" x="136" y="51" width="38" height="14" rx="3" />
    </svg>
  );
}

/** A walk through the catalog: solid where visited, dashed where untouched. */
export function DiscoveryDiagram(): React.ReactElement {
  return (
    <svg className="l-dg" width="200" height="80" viewBox="0 0 240 96" aria-hidden="true">
      <g
        className="l-dg-meta"
        fill="none"
        strokeWidth="1.6"
        strokeDasharray="3 3"
        opacity=".85"
      >
        <path d="M26 48h20c10 0 8-28 18-28" />
        <path d="M26 48h20c10 0 8 28 18 28" />
        <path d="M105 46c12-2 10-18 22-18h5" />
        <path d="M157 76c14 2 12 14 26 14h7" />
      </g>
      <g className="l-dg-meta" fill="none" strokeWidth="1.6" strokeDasharray="2 2" opacity=".8">
        <circle cx="72" cy="20" r="7" />
        <circle cx="72" cy="76" r="7" />
        <circle cx="140" cy="28" r="7" />
        <circle cx="196" cy="90" r="6" />
      </g>
      <g className="l-dg-ink" fill="none" strokeWidth="1.8">
        <path d="M26 48h62m-6-5l6 5-6 5" />
      </g>
      <g className="l-dg-ink" fill="none" strokeWidth="1.8">
        <path d="M105 50c12 4 10 24 22 24h9m-6-5l6 5-6 5" />
        <path d="M157 72c10 0 12-6 20-6h4m-5-4l5 4-5 4" />
      </g>
      <circle className="l-dg-ink-fill" cx="16" cy="48" r="9" />
      <circle className="l-dg-ink-fill" cx="97" cy="48" r="7" />
      <circle className="l-dg-ink-fill" cx="149" cy="74" r="7" />
      <circle className="l-dg-ink-fill" cx="190" cy="66" r="7" />
    </svg>
  );
}

/*
 * Three overlapping layers. One mask per circle hides the two circles it
 * overlaps, so the masked copy draws a solid arc outside the overlaps while the
 * dashed copy underneath shows through inside them. The mask fills are coverage
 * values on the alpha channel rather than theme colours, which is why they stay
 * white and black.
 */
const LAYER_MASKS = ["l-dg-layers-0", "l-dg-layers-1", "l-dg-layers-2"] as const;

/** Three overlapping source layers merging into one catalog. */
export function LayersDiagram(): React.ReactElement {
  return (
    <svg className="l-dg" width="200" height="97" viewBox="0 0 240 116" aria-hidden="true">
      <defs>
        <mask id={LAYER_MASKS[0]} maskUnits="userSpaceOnUse" x="0" y="0" width="240" height="116">
          <rect x="0" y="0" width="240" height="116" fill="white" />
          <circle cx="126" cy="41" r="26" fill="black" />
          <circle cx="107" cy="69" r="26" fill="black" />
        </mask>
        <mask id={LAYER_MASKS[1]} maskUnits="userSpaceOnUse" x="0" y="0" width="240" height="116">
          <rect x="0" y="0" width="240" height="116" fill="white" />
          <circle cx="88" cy="41" r="26" fill="black" />
          <circle cx="107" cy="69" r="26" fill="black" />
        </mask>
        <mask id={LAYER_MASKS[2]} maskUnits="userSpaceOnUse" x="0" y="0" width="240" height="116">
          <rect x="0" y="0" width="240" height="116" fill="white" />
          <circle cx="88" cy="41" r="26" fill="black" />
          <circle cx="126" cy="41" r="26" fill="black" />
        </mask>
      </defs>
      <g className="l-dg-ink-fill" opacity=".12">
        <circle cx="88" cy="41" r="26" />
        <circle cx="126" cy="41" r="26" />
        <circle cx="107" cy="69" r="26" />
      </g>
      <g className="l-dg-meta" fill="none" strokeWidth="1.5" strokeDasharray="3 3" opacity=".85">
        <circle cx="88" cy="41" r="26" />
        <circle cx="126" cy="41" r="26" />
        <circle cx="107" cy="69" r="26" />
      </g>
      <circle
        className="l-dg-meta"
        cx="88"
        cy="41"
        r="26"
        fill="none"
        strokeWidth="1.5"
        opacity=".7"
        mask={`url(#${LAYER_MASKS[0]})`}
      />
      <circle
        className="l-dg-meta"
        cx="126"
        cy="41"
        r="26"
        fill="none"
        strokeWidth="1.5"
        opacity=".7"
        mask={`url(#${LAYER_MASKS[1]})`}
      />
      <circle
        className="l-dg-meta"
        cx="107"
        cy="69"
        r="26"
        fill="none"
        strokeWidth="1.5"
        opacity=".7"
        mask={`url(#${LAYER_MASKS[2]})`}
      />
      <g className="l-dg-mono l-dg-meta-fill" fontSize="10">
        <text x="48" y="15" textAnchor="middle">
          team
        </text>
        <text x="166" y="15" textAnchor="middle">
          org
        </text>
        <text x="107" y="109" textAnchor="middle">
          personal
        </text>
      </g>
    </svg>
  );
}

/** A catalog whose locked rows stay behind and whose visible pair travels. */
export function AccessDiagram(): React.ReactElement {
  return (
    <svg className="l-dg" width="200" height="80" viewBox="0 0 240 96" aria-hidden="true">
      <g className="l-dg-meta" strokeWidth="1.6" strokeDasharray="5 4" fill="none">
        <rect x="4" y="8" width="96" height="80" rx="5" />
      </g>
      <rect
        className="l-dg-meta"
        x="12"
        y="14"
        width="38"
        height="14"
        rx="3"
        fill="none"
        strokeWidth="1.4"
        opacity=".45"
      />
      <Padlock x={37} y={20.2} />
      <rect className="l-dg-accent-fill" x="56" y="14" width="38" height="14" rx="3" />
      <rect
        className="l-dg-meta"
        x="12"
        y="32"
        width="38"
        height="14"
        rx="3"
        fill="none"
        strokeWidth="1.4"
        opacity=".45"
      />
      <Padlock x={37} y={38.2} />
      <rect
        className="l-dg-meta"
        x="56"
        y="32"
        width="38"
        height="14"
        rx="3"
        fill="none"
        strokeWidth="1.4"
        opacity=".45"
      />
      <Padlock x={81} y={38.2} />
      <rect className="l-dg-accent-fill" x="12" y="50" width="38" height="14" rx="3" />
      <rect
        className="l-dg-meta"
        x="56"
        y="50"
        width="38"
        height="14"
        rx="3"
        fill="none"
        strokeWidth="1.4"
        opacity=".45"
      />
      <Padlock x={81} y={56.2} />
      <rect
        className="l-dg-meta"
        x="12"
        y="68"
        width="38"
        height="14"
        rx="3"
        fill="none"
        strokeWidth="1.4"
        opacity=".45"
      />
      <Padlock x={37} y={74.2} />
      <rect
        className="l-dg-meta"
        x="56"
        y="68"
        width="38"
        height="14"
        rx="3"
        fill="none"
        strokeWidth="1.4"
        opacity=".45"
      />
      <Padlock x={81} y={74.2} />
      <path className="l-dg-meta" d="M108 48h16m-5-4l5 4-5 4" strokeWidth="1.6" fill="none" />
      <rect className="l-dg-accent-fill" x="136" y="31" width="38" height="14" rx="3" />
      <rect className="l-dg-accent-fill" x="136" y="51" width="38" height="14" rx="3" />
    </svg>
  );
}

/**
 * The padlock glyph the access diagram repeats. The shackle sits 1.4 units above
 * the body's top-left corner, which is what the reference draws at each row.
 */
function Padlock(props: { x: number; y: number }): React.ReactElement {
  const { x, y } = props;
  // Rounded to one decimal, because the offsets are exact in the reference and
  // the binary sum of 20.2 and 2.3 is not.
  const shackleX = Math.round((x + 1.4) * 10) / 10;
  const shackleY = Math.round((y + 2.3) * 10) / 10;

  return (
    <>
      <path
        className="l-dg-meta"
        d={`M${shackleX} ${shackleY}v-1.6a2.1 2.1 0 014.2 0v1.6`}
        fill="none"
        strokeWidth="1.4"
      />
      <rect className="l-dg-meta-fill" x={x} y={y} width="7" height="5.4" rx="1.2" />
    </>
  );
}

/** The diagram each feature card names, keyed by the content model's value. */
export const featureDiagrams: Record<FeatureDiagramKey, () => React.ReactElement> = {
  delivery: DeliveryDiagram,
  domains: DomainsDiagram,
  materialization: MaterializationDiagram,
  discovery: DiscoveryDiagram,
  layers: LayersDiagram,
  access: AccessDiagram,
};

/* -------------------------------------------------------- deployment icons */

/** A folder, marking the deployment that is one directory on disk. */
export function FolderIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="17" height="15" viewBox="0 0 20 17" aria-hidden="true">
      <path className="l-dg-accent" d="M1 4.5h6l2 2.5h10V16H1z" fill="none" strokeWidth="1.6" />
    </svg>
  );
}

/** A database cylinder, marking the deployment that runs one server. */
export function DatabaseIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="16" height="17" viewBox="0 0 18 19" aria-hidden="true">
      <g className="l-dg-accent" fill="none" strokeWidth="1.6">
        <ellipse cx="9" cy="4" rx="6.6" ry="2.7" />
        <path d="M2.4 4v11c0 1.5 3 2.7 6.6 2.7s6.6-1.2 6.6-2.7V4" />
        <path d="M2.4 9.5c0 1.5 3 2.7 6.6 2.7s6.6-1.2 6.6-2.7" />
      </g>
    </svg>
  );
}

/** A six-spoke asterisk, marking the deployment that runs several replicas. */
export function AsteriskIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="17" height="17" viewBox="0 0 18 18" aria-hidden="true">
      <g className="l-dg-accent" strokeWidth="1.6" strokeLinecap="round">
        <path d="M9 2.2v13.6M3.1 5.6l11.8 6.8M14.9 5.6L3.1 12.4" />
      </g>
      <g className="l-dg-accent-fill">
        <circle cx="9" cy="2.6" r="1.7" />
        <circle cx="9" cy="15.4" r="1.7" />
        <circle cx="3.4" cy="5.8" r="1.7" />
        <circle cx="14.6" cy="12.2" r="1.7" />
        <circle cx="14.6" cy="5.8" r="1.7" />
        <circle cx="3.4" cy="12.2" r="1.7" />
      </g>
    </svg>
  );
}

/** The icon each deployment card names, keyed by the content model's value. */
export const deploymentIcons: Record<DeploymentIconKey, () => React.ReactElement> = {
  folder: FolderIcon,
  database: DatabaseIcon,
  asterisk: AsteriskIcon,
};

/* ------------------------------------------------------- integration icons */

/** A cylinder, for the metadata store row. */
export function CylinderIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
      <g className="l-dg-meta" fill="none" strokeWidth="1.5">
        <ellipse cx="8" cy="4" rx="6" ry="2.4" />
        <path d="M2 4v8c0 1.3 2.7 2.4 6 2.4s6-1.1 6-2.4V4" />
        <path d="M2 8.4c0 1.3 2.7 2.4 6 2.4s6-1.1 6-2.4" />
      </g>
    </svg>
  );
}

/** A cube, for the object storage row. */
export function CubeIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="15" height="16" viewBox="0 0 16 17" aria-hidden="true">
      <g className="l-dg-meta" fill="none" strokeWidth="1.5">
        <path d="M8 1.2l6 3v8.6l-6 3-6-3V4.2z" />
        <path d="M2 4.2l6 3 6-3M8 7.2v8.6" />
      </g>
    </svg>
  );
}

/** Three linked nodes, for the vector index row. */
export function NodesIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
      <g className="l-dg-meta" fill="none" strokeWidth="1.5">
        <circle cx="8" cy="2.6" r="1.8" />
        <circle cx="2.6" cy="13" r="1.8" />
        <circle cx="13.4" cy="13" r="1.8" />
      </g>
      <path
        className="l-dg-meta"
        d="M7 4.2L3.6 11.3M9 4.2l3.4 7.1M4.5 13h7"
        fill="none"
        strokeWidth="1.4"
      />
    </svg>
  );
}

/** Two sparkles, for the embeddings row. */
export function SparkleIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
      <g className="l-dg-meta-fill">
        <path d="M4 1.5l1.2 2.8L8 5.5 5.2 6.7 4 9.5 2.8 6.7 0 5.5l2.8-1.2z" />
        <path d="M11 7l.9 2.1 2.1.9-2.1.9-.9 2.1-.9-2.1L8 10l2.1-.9z" />
      </g>
    </svg>
  );
}

/** A padlock, for the identity row. */
export function PadlockIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="14" height="16" viewBox="0 0 15 17" aria-hidden="true">
      <g className="l-dg-meta" fill="none" strokeWidth="1.5">
        <rect x="2" y="7" width="11" height="8.5" rx="2" />
        <path d="M4.8 7V5a2.7 2.7 0 015.4 0v2" />
      </g>
    </svg>
  );
}

/** A branch, for the layer sources row. */
export function BranchIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="15" height="16" viewBox="0 0 16 17" aria-hidden="true">
      <g className="l-dg-meta" fill="none" strokeWidth="1.5">
        <circle cx="4" cy="3.2" r="2.1" />
        <circle cx="4" cy="13.8" r="2.1" />
        <circle cx="12" cy="8.5" r="2.1" />
      </g>
      <path
        className="l-dg-meta"
        d="M4 5.3v6.4M6.1 4.2c3 .6 3.9 2 3.9 4.3M6.1 12.8c3-.6 3.9-2 3.9-4.3"
        fill="none"
        strokeWidth="1.4"
      />
    </svg>
  );
}

/** The icon each integration row names, keyed by the content model's value. */
export const integrationIcons: Record<IntegrationIconKey, () => React.ReactElement> = {
  cylinder: CylinderIcon,
  cube: CubeIcon,
  nodes: NodesIcon,
  sparkle: SparkleIcon,
  padlock: PadlockIcon,
  branch: BranchIcon,
};
