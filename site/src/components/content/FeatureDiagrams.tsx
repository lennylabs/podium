import type * as React from "react";

/*
 * The landing page's inline SVG artwork: the feature diagrams, the deployment
 * icons, and the server-side integration icons.
 *
 * The geometry is transcribed from web/design/site/podium-design-reference.html.
 * The reference carries one copy per theme with hard-coded fills; this module
 * carries one copy whose colours come from classes defined in landing.css, so
 * a single set of markup serves both themes:
 *
 *   .l-dg-ink / .l-dg-ink-fill      primary shapes
 *   .l-dg-meta / .l-dg-meta-fill    connectors and de-emphasized shapes
 *   .l-dg-accent / .l-dg-accent-fill  selected and visible states
 *   .l-dg-tick                      a glyph drawn on top of an accent fill
 *   .l-dg-on-accent-fill            text set on top of an accent fill
 *   .l-dg-paper                     the panel colour behind a primary shape
 *
 * The diagrams carry no masks and no ids, so no id can collide when a diagram
 * renders more than once in a document.
 *
 * Every diagram is decorative and carries aria-hidden, because the card text
 * beside it states the same thing in prose.
 */

/** Names the diagram a feature card shows. */
export type FeatureDiagramKey =
  | "domains"
  | "materialization"
  | "layers"
  | "security"
  | "usage"
  | "overlap";

/** Names the icon a deployment card shows. */
export type DeploymentIconKey = "folder" | "database" | "asterisk";

/** Names the icon an integration row shows. */
export type IntegrationIconKey =
  | "cylinder"
  | "cube"
  | "nodes"
  | "sparkle"
  | "circles"
  | "padlock"
  | "branch"
  | "scroll"
  | "pulse"
  | "bell";

/* --------------------------------------------------------- feature diagrams */

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

/** Three flat layers seen at an angle, stacked, and joined at their corners. */
export function LayersDiagram(): React.ReactElement {
  return (
    <svg className="l-dg" width="200" height="93" viewBox="0 0 240 112" aria-hidden="true">
      <g className="l-dg-paper l-dg-ink" strokeWidth="1.6" strokeLinejoin="round">
        <path d="M24 78 L100 64 L176 78 L100 92 Z" />
        <path d="M24 54 L100 40 L176 54 L100 68 Z" />
        <path d="M24 30 L100 16 L176 30 L100 44 Z" />
      </g>
      <path
        className="l-dg-meta"
        d="M24 30v48M176 30v48"
        strokeWidth="1"
        strokeDasharray="2 2"
      />
      <g className="l-dg-mono l-dg-meta-fill" fontSize="9">
        <text x="186" y="33">
          personal
        </text>
        <text x="186" y="57">
          team
        </text>
        <text x="186" y="81">
          org
        </text>
      </g>
    </svg>
  );
}

/** A shield outline carrying a check in the accent. */
export function SecurityDiagram(): React.ReactElement {
  return (
    <svg className="l-dg" width="200" height="80" viewBox="0 0 240 96" aria-hidden="true">
      <path
        className="l-dg-paper l-dg-ink"
        d="M120 6 L154 17 V44 C154 66 139 81 120 90 C101 81 86 66 86 44 V17 Z"
        strokeWidth="1.8"
        strokeLinejoin="round"
      />
      <path
        className="l-dg-accent"
        d="M105 47l10 10 21-22"
        fill="none"
        strokeWidth="3.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/*
 * The usage rows, ranked from the most used down. Each row is a label stub, a
 * bar, and the count printed after the bar's end.
 */
const USAGE_ROWS = [
  { y: 8, label: 44, bar: 132, count: "412" },
  { y: 30, label: 36, bar: 98, count: "305" },
  { y: 52, label: 50, bar: 70, count: "214" },
  { y: 74, label: 30, bar: 44, count: "131" },
] as const;

/** Ranked usage: the top bar in the accent, the rest muted. */
export function UsageDiagram(): React.ReactElement {
  return (
    <svg className="l-dg" width="200" height="80" viewBox="0 0 240 96" aria-hidden="true">
      <g className="l-dg-mono" fontSize="9.5">
        {USAGE_ROWS.map((row, rank) => {
          const top = rank === 0;

          return (
            <g key={row.count}>
              <rect
                className="l-dg-ink-fill"
                x="20"
                y={row.y + 6}
                width={row.label}
                height="3"
                rx="1.5"
                opacity=".55"
              />
              <rect
                className={top ? "l-dg-accent-fill" : "l-dg-meta-fill"}
                x="80"
                y={row.y}
                width={row.bar}
                height="12"
                rx="3"
                opacity={top ? undefined : ".3"}
              />
              <text
                className={top ? "l-dg-ink-fill" : "l-dg-meta-fill"}
                x={80 + row.bar + 6}
                y={row.y + 9.5}
                fontWeight={top ? "600" : "400"}
              >
                {row.count}
              </text>
            </g>
          );
        })}
      </g>
    </svg>
  );
}

/**
 * Two skill cards overlapping at a corner. The shared area and the line that
 * runs through both cards are in the accent, and the pill above carries the
 * similarity score.
 */
export function OverlapDiagram(): React.ReactElement {
  return (
    <svg className="l-dg" width="200" height="80" viewBox="0 0 240 96" aria-hidden="true">
      <rect className="l-dg-paper" x="34" y="6" width="104" height="58" rx="5" />
      <rect className="l-dg-paper" x="98" y="30" width="104" height="58" rx="5" />
      <rect className="l-dg-accent-fill" x="98" y="30" width="40" height="34" opacity=".4" />
      <g className="l-dg-ink" fill="none" strokeWidth="1.6">
        <rect x="34" y="6" width="104" height="58" rx="5" />
        <rect x="98" y="30" width="104" height="58" rx="5" />
      </g>
      <g className="l-dg-ink-fill" opacity=".5">
        <rect x="44" y="17" width="48" height="3" rx="1.5" />
        <rect x="44" y="29" width="36" height="3" rx="1.5" />
        <rect x="146" y="61" width="46" height="3" rx="1.5" />
        <rect x="108" y="73" width="60" height="3" rx="1.5" />
      </g>
      <g className="l-dg-accent-fill">
        <rect x="44" y="45" width="86" height="4" rx="2" />
        <rect x="106" y="45" width="86" height="4" rx="2" />
        <rect x="160" y="8" width="42" height="15" rx="7.5" />
      </g>
      <text
        className="l-dg-mono l-dg-on-accent-fill"
        x="181"
        y="19"
        fontSize="9.5"
        fontWeight="600"
        textAnchor="middle"
      >
        0.91
      </text>
    </svg>
  );
}

/** The diagram each feature card names, keyed by the content model's value. */
export const featureDiagrams: Record<FeatureDiagramKey, () => React.ReactElement> = {
  domains: DomainsDiagram,
  materialization: MaterializationDiagram,
  layers: LayersDiagram,
  security: SecurityDiagram,
  usage: UsageDiagram,
  overlap: OverlapDiagram,
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

/** Two overlapping circles, for the overlap detection row. */
export function CirclesIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
      <g className="l-dg-meta" fill="none" strokeWidth="1.5">
        <circle cx="5.8" cy="8" r="4.6" />
        <circle cx="10.2" cy="8" r="4.6" />
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

/** A page with ruled lines, for the audit log row. */
export function ScrollIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
      <path
        className="l-dg-meta"
        d="M3.5 2h8.3a1.5 1.5 0 0 1 1.5 1.5V14H5a1.5 1.5 0 0 1-1.5-1.5z"
        fill="none"
        strokeWidth="1.5"
      />
      <path className="l-dg-meta" d="M6.2 5.6h4.6M6.2 8.4h4.6M6.2 11.2h2.8" strokeWidth="1.4" />
    </svg>
  );
}

/** A pulse line, for the tracing row. */
export function PulseIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
      <path
        className="l-dg-meta"
        d="M1 8.5h3.2l2-5.3 3.2 9.6 2-4.3H15"
        fill="none"
        strokeWidth="1.5"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}

/** A bell, for the notifications row. */
export function BellIcon(): React.ReactElement {
  return (
    <svg className="l-dg" width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
      <path
        className="l-dg-meta"
        d="M4 11.3V7.2a4 4 0 0 1 8 0v4.1l1.4 1.5H2.6z"
        fill="none"
        strokeWidth="1.5"
        strokeLinejoin="round"
      />
      <path
        className="l-dg-meta"
        d="M6.4 14.4a1.7 1.7 0 0 0 3.2 0"
        fill="none"
        strokeWidth="1.5"
        strokeLinecap="round"
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
  circles: CirclesIcon,
  padlock: PadlockIcon,
  branch: BranchIcon,
  scroll: ScrollIcon,
  pulse: PulseIcon,
  bell: BellIcon,
};
