/*
 * Landing page copy, held as typed data so the page component contains layout
 * and no prose. Every string the landing design shows lives here.
 *
 * Link targets are stored as routes, not as finished URLs. The page component
 * resolves a "doc" target against SiteConfig.basePath and a "repo" target
 * against SiteConfig.repoUrl, so a base path change lands in one place.
 */

import type {
  DeploymentIconKey,
  FeatureDiagramKey,
  IntegrationIconKey,
} from "../components/content/FeatureDiagrams";

/** Where a landing link points, before the site config resolves it. */
export type LinkTarget =
  /** A published documentation route, leading slash included. */
  | { kind: "doc"; route: string }
  /** A path appended to the repository URL, empty for the repository root. */
  | { kind: "repo"; path: string };

export type NavLink = {
  label: string;
  target: LinkTarget;
  /** The primary destination in the bar, rendered at full ink and weight 500. */
  emphasized: boolean;
};

/** A run of copy that either carries the highlighter treatment or does not. */
export type MarkedText = {
  text: string;
  marked: boolean;
};

export type StatusPill = {
  text: string;
};

export type InstallCommand = {
  /** The shell prompt glyph shown before the command. */
  prompt: string;
  command: string;
  /** The resting label on the copy control. */
  copyLabel: string;
  /** The label the copy script swaps in after a successful copy. */
  copiedLabel: string;
};

export type ActionVariant = "primary" | "outline" | "ghost";

export type Action = {
  label: string;
  target: LinkTarget;
  variant: ActionVariant;
};

/**
 * One line of the hero transcript. Each line renders as its own block element,
 * so the transcript never depends on newlines inside a preformatted block.
 *
 * A "command" line renders behind a prompt glyph, with its flag run in the link
 * tone. A "path" line is a materialized file path, indented under the command
 * that wrote it. A "gap" line is the blank line between two invocations, and a
 * "cursor" line is the trailing prompt and its blinking block.
 */
export type TerminalLine =
  | { kind: "command"; command: string; flag: string }
  | { kind: "path"; text: string }
  | { kind: "gap" }
  | { kind: "cursor" };

export type Terminal = {
  /** The working directory shown in the title bar. */
  directory: string;
  prompt: string;
  /** Describes the transcript for assistive technology. */
  caption: string;
  lines: TerminalLine[];
};

export type FeatureCard = {
  /** Two-digit ordinal, shown in mono above the title. */
  number: string;
  title: string;
  body: string;
  /** The decorative diagram drawn to the left of the text. */
  diagram: FeatureDiagramKey;
};

/** One label and value pair inside a deployment card. */
export type DeploymentFact = {
  label: string;
  value: string;
};

export type DeploymentCard = {
  icon: DeploymentIconKey;
  title: string;
  facts: DeploymentFact[];
  /** Heading over the bullet list, null on the card that inherits nothing. */
  plusLabel: string | null;
  /** Capabilities the card adds, each behind an accent bullet. */
  plus: string[];
};

export type Callout = {
  /** The leading glyph, drawn in the link tone. */
  glyph: string;
  text: string;
};

/** One row of the server-side integrations table. */
export type IntegrationRow = {
  icon: IntegrationIconKey;
  name: string;
  /** What Podium ships with for this concern. */
  builtIn: string;
  /** Backends that can replace it, each rendered as a tinted pill. */
  alternatives: string[];
  /** A qualification shown under the pills, or null. */
  note: string | null;
};

export type FooterLink = {
  label: string;
  target: LinkTarget;
};

export type LandingContent = {
  nav: {
    /** The wordmark beside the logo. */
    wordmark: string;
    home: LinkTarget;
    links: NavLink[];
    /** Prefix read out before the version, hidden visually. */
    versionLabel: string;
    /** Prefix printed in front of the version number. */
    versionPrefix: string;
  };
  hero: {
    status: StatusPill;
    /** One entry per headline line. The marked entry takes the highlighter. */
    headline: MarkedText[];
    /** Inline runs of the subtitle, in order. */
    subtitle: MarkedText[];
    install: InstallCommand;
    actions: Action[];
    terminal: Terminal;
  };
  adapters: {
    label: string;
    names: string[];
    /** The trailing, de-emphasized entry. */
    custom: string;
  };
  features: {
    heading: string;
    cards: FeatureCard[];
  };
  deployment: {
    heading: string;
    cards: DeploymentCard[];
    callout: Callout;
  };
  integrations: {
    heading: string;
    /** Column headings. The first is read out but not drawn. */
    columns: {
      subject: string;
      builtIn: string;
      alternatives: string;
    };
    rows: IntegrationRow[];
    /** The muted line that closes the section. */
    note: string;
  };
  footer: {
    license: string;
    /** Rendered between the footer's left-hand items. */
    separator: string;
    repo: FooterLink;
    links: FooterLink[];
  };
};

export const landing: LandingContent = {
  nav: {
    wordmark: "Podium",
    home: { kind: "doc", route: "/" },
    links: [
      {
        label: "Docs",
        target: { kind: "doc", route: "/getting-started/index.html" },
        emphasized: true,
      },
      {
        label: "GitHub",
        target: { kind: "repo", path: "" },
        emphasized: false,
      },
    ],
    versionLabel: "Podium version",
    versionPrefix: "v",
  },
  hero: {
    status: {
      text: "0.3.x — early release",
    },
    headline: [
      { text: "One catalog.", marked: false },
      { text: "Every harness.", marked: true },
    ],
    subtitle: [
      { text: "A catalog for reusable ", marked: false },
      { text: "AI skills and other artifacts", marked: true },
      {
        text: ", with tools to translate them into harness-specific formats.",
        marked: false,
      },
    ],
    install: {
      prompt: "$",
      command: "brew install lennylabs/tap/podium",
      copyLabel: "copy",
      copiedLabel: "copied",
    },
    actions: [
      {
        label: "Quickstart",
        target: { kind: "doc", route: "/getting-started/quickstart.html" },
        variant: "primary",
      },
      {
        label: "Concepts",
        target: { kind: "doc", route: "/getting-started/concepts.html" },
        variant: "outline",
      },
      {
        label: "Fit & comparisons",
        target: { kind: "doc", route: "/getting-started/why-podium.html" },
        variant: "ghost",
      },
    ],
    terminal: {
      directory: "~/projects/foo",
      prompt: "$",
      caption:
        "Terminal transcript. Three podium sync runs materialize the same artifacts three ways: into the Claude Code layout, into the Cursor layout, and into the marketplace layout a shared configuration file describes.",
      lines: [
        { kind: "command", command: "podium sync", flag: "--harness claude-code" },
        { kind: "path", text: ".claude/skills/rollback/SKILL.md" },
        { kind: "path", text: ".claude/skills/rollback/scripts/verify_revision.py" },
        { kind: "path", text: ".claude/commands/query-review.md" },
        { kind: "path", text: ".mcp.json" },
        { kind: "gap" },
        { kind: "command", command: "podium sync", flag: "--harness cursor" },
        { kind: "path", text: ".cursor/skills/rollback/SKILL.md" },
        { kind: "path", text: ".cursor/skills/rollback/scripts/verify_revision.py" },
        { kind: "path", text: ".cursor/commands/query-review.md" },
        { kind: "path", text: ".cursor/mcp.json" },
        { kind: "gap" },
        { kind: "command", command: "podium sync", flag: "--config marketplace.yaml" },
        { kind: "path", text: ".claude-plugin/marketplace.json" },
        { kind: "path", text: "claude/acme/skills/rollback/SKILL.md" },
        { kind: "path", text: "claude/acme/skills/rollback/scripts/verify_revision.py" },
        { kind: "path", text: "cursor/acme/skills/rollback/SKILL.md" },
        { kind: "gap" },
        { kind: "cursor" },
      ],
    },
  },
  adapters: {
    label: "ADAPTERS",
    names: [
      "Claude Code",
      "Claude Desktop",
      "Claude Cowork",
      "Cursor",
      "Codex",
      "Gemini CLI",
      "OpenCode",
      "Pi",
      "Hermes",
    ],
    custom: "+ custom",
  },
  features: {
    heading: "Features",
    cards: [
      {
        number: "01",
        title: "Cross-harness delivery",
        body: "Write canonical artifacts once. Automatically translate into the format your runtime expects.",
        diagram: "delivery",
      },
      {
        number: "02",
        title: "Domains and subdomains",
        body: "Easy artifact maintenance and discoverability through the use of domains and sub-domains.",
        diagram: "domains",
      },
      {
        number: "03",
        title: "Selective materialization",
        body: "Sync a subset of the catalog into a workspace. Define profiles to quickly and seamlessly switch between scopes.",
        diagram: "materialization",
      },
      {
        number: "04",
        title: "Progressive discovery",
        body: "Minimalistic set of tools for agents to traverse domains and find artifacts, materializing artifacts (including bundled files) lazily as they are needed.",
        diagram: "discovery",
      },
      {
        number: "05",
        title: "Layered composition",
        body: "Compose the catalog from multiple independent sources with deterministic merge and explicit precedence.",
        diagram: "layers",
      },
      {
        number: "06",
        title: "Access control",
        body: "Declare who can see what: public, org-wide, scoped to OIDC groups, or specific users.",
        diagram: "access",
      },
    ],
  },
  deployment: {
    heading: "Run it three ways",
    cards: [
      {
        icon: "folder",
        title: "Local",
        facts: [
          { label: "Server-side deployment", value: "None" },
          { label: "Catalog source", value: "A folder, read from disk" },
          { label: "Materialization", value: "User-driven sync" },
        ],
        plusLabel: null,
        plus: ["Author, lint, sync", "Domains and profiles", "Ordered layers from disk"],
      },
      {
        icon: "database",
        title: "Single node",
        facts: [
          { label: "Server-side deployment", value: "One binary" },
          {
            label: "Catalog source",
            value: "One or more folders or remote Git repos",
          },
          {
            label: "Materialization",
            value: "User-driven sync, or agent-driven on demand",
          },
        ],
        plusLabel: "Everything in local, plus",
        plus: [
          "Discovery via MCP or SDK",
          "Hybrid search",
          "Registered and remote layers, with visibility",
          "One audit log",
        ],
      },
      {
        icon: "asterisk",
        title: "Clustered",
        facts: [
          { label: "Server-side deployment", value: "Replicas, Postgres, storage" },
          {
            label: "Catalog source",
            value: "One or more folders or remote Git repos",
          },
          {
            label: "Materialization",
            value: "User-driven sync, or agent-driven on demand",
          },
        ],
        plusLabel: "Everything in single node, plus",
        plus: [
          "Multi-tenancy",
          "SCIM group sync",
          "Signing and transparency log",
          "High availability",
        ],
      },
    ],
    callout: {
      glyph: "→",
      text: "The artifacts never change. They are independent of Podium's deployment model.",
    },
  },
  integrations: {
    heading: "Server-side integrations",
    columns: {
      subject: "Integration",
      builtIn: "Out of the box",
      alternatives: "Compatible alternatives",
    },
    rows: [
      {
        icon: "cylinder",
        name: "Metadata store",
        builtIn: "SQLite",
        alternatives: ["Postgres"],
        note: null,
      },
      {
        icon: "cube",
        name: "Object storage",
        builtIn: "Local filesystem",
        alternatives: ["S3 or S3-compatible storage"],
        note: null,
      },
      {
        icon: "nodes",
        name: "Vector index",
        builtIn: "sqlite-vec",
        alternatives: ["pgvector", "Pinecone", "Weaviate Cloud", "Qdrant Cloud"],
        note: "pgvector is the default when you already run Postgres.",
      },
      {
        icon: "sparkle",
        name: "Embeddings",
        builtIn: "BM25 only",
        alternatives: ["OpenAI", "Voyage", "Cohere", "Ollama"],
        note: "A provider is configured by default (ollama, or openai on Postgres) but ships in no binary, so hybrid search needs it reachable. Or let Pinecone, Weaviate, or Qdrant embed on ingest. Vectors are fused with BM25 hits via reciprocal rank fusion.",
      },
      {
        icon: "padlock",
        name: "Identity",
        builtIn: "None",
        alternatives: ["oidc-jwt", "trusted-headers", "injected-session-token"],
        note: "SCIM provisions groups alongside a provider rather than replacing one.",
      },
      {
        icon: "branch",
        name: "Layer sources",
        builtIn: "Git and local paths",
        alternatives: ["Custom via SPI"],
        note: "S3 buckets, OCI registries, HTTP archives.",
      },
    ],
    note: "Nothing in the right column is required to start. At cluster scale, Postgres and object storage become requirements. Easily re-embed during vector backend migrations.",
  },
  footer: {
    license: "MIT licensed",
    separator: "·",
    repo: { label: "lennylabs/podium", target: { kind: "repo", path: "" } },
    links: [
      { label: "Docs", target: { kind: "doc", route: "/getting-started/index.html" } },
      { label: "Governance", target: { kind: "doc", route: "/about/governance.html" } },
      { label: "Contributing", target: { kind: "doc", route: "/about/contributing.html" } },
      { label: "Changelog", target: { kind: "doc", route: "/about/changelog.html" } },
    ],
  },
};
