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
 * A run of transcript text in one tone. "ink" is a command name or an artifact
 * ID, "arg" is a flag, an argument, or a [type] tag in the link tone, and "out"
 * is a description, a label, or a path in the body tone.
 */
export type TerminalSpan = {
  text: string;
  tone: "ink" | "arg" | "out";
};

/**
 * One line of the hero transcript. Each line renders as its own block element,
 * so the transcript never depends on newlines inside a preformatted block, and
 * the leading spaces each span carries survive.
 *
 * A "command" line is set in the heavier weight, behind a prompt glyph unless
 * it continues the previous line. An "output" line is what the command
 * printed. A "gap" is vertical space: under a command, under a results header,
 * or between two invocations. A "cursor" line is the trailing prompt and its
 * blinking block.
 */
export type TerminalLine =
  | { kind: "command"; prompt: boolean; spans: TerminalSpan[] }
  | { kind: "output"; spans: TerminalSpan[] }
  | { kind: "gap"; size: "command" | "header" | "block" }
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
  /** One or more paragraphs, rendered 8px apart. */
  body: string[];
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
  /** A short pill at the right of the title row, or null. */
  badge: string | null;
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
    /** Accessible name for the mobile menu that holds the nav links. */
    menuLabel: string;
    /** Prefix printed in front of the version number. */
    versionPrefix: string;
  };
  hero: {
    /** One entry per headline line, each a list of runs. A marked run takes
        the highlighter. */
    headline: MarkedText[][];
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
    menuLabel: "Menu",
    versionPrefix: "v",
  },
  hero: {
    headline: [
      [
        { text: "The ", marked: false },
        { text: "skill registry", marked: true },
      ],
      [{ text: "for AI agents.", marked: false }],
    ],
    subtitle: [
      { text: "An enterprise-grade registry for reusable ", marked: false },
      { text: "agent skills and other artifacts", marked: true },
      { text: ".", marked: false },
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
        "Terminal transcript. podium search finds three artifacts for an incident rollback, podium domain show lists the subdomains of the platform domain, and podium sync writes the rollback skill and its bundled script into the Claude Code layout.",
      lines: [
        command(true, ink("podium search "), arg('"incident rollback"')),
        { kind: "gap", size: "command" },
        output(out("Showing 3 of 3 results")),
        { kind: "gap", size: "header" },
        output(ink("  platform/rollback  "), arg("[skill]")),
        output(out("      Roll back a bad deploy and verify the running revision.")),
        output(ink("  platform/k8s/pod-forensics  "), arg("[skill]")),
        output(out("      Capture pod state and logs before a rollback.")),
        output(ink("  platform/incident-commander  "), arg("[agent]")),
        output(out("      Coordinate incident response and rollback decisions.")),
        { kind: "gap", size: "block" },
        command(true, ink("podium domain show "), arg("platform")),
        { kind: "gap", size: "command" },
        output(ink("platform")),
        output(out("  Platform operations and incident response.")),
        output(out("subdomains:")),
        output(ink("  - platform/ci"), out("  ci")),
        output(out("      Build and release pipelines.")),
        output(ink("  - platform/k8s"), out("  k8s")),
        output(out("      Kubernetes operations.")),
        { kind: "gap", size: "block" },
        command(true, ink("podium sync "), arg("--harness claude-code"), ink(" \\")),
        command(false, ink("              "), arg("--include platform/rollback")),
        { kind: "gap", size: "command" },
        output(out("adapter: claude-code")),
        output(out("target:  /Users/alice/projects/foo")),
        output(out("artifacts:")),
        output(ink("  - platform/rollback"), out("  [team-platform]")),
        output(out("      .claude/skills/rollback/SKILL.md")),
        output(out("      .claude/skills/rollback/scripts/verify_revision.py")),
        { kind: "gap", size: "block" },
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
        title: "Domains and subdomains",
        body: [
          "Easy skill maintenance and discoverability through the use of domains and sub-domains.",
        ],
        diagram: "domains",
      },
      {
        number: "02",
        title: "Selective materialization",
        body: [
          "Sync a chosen subset of the catalog into a workspace ahead of time. Define profiles to quickly and seamlessly switch between scopes.",
        ],
        diagram: "materialization",
      },
      {
        number: "03",
        title: "Progressive discovery",
        body: [
          "stdio MCP server with tools for agents to find skills and to lazily load them into the agent's workspace (including bundled files).",
        ],
        diagram: "discovery",
      },
      {
        number: "04",
        title: "Layered composition",
        body: [
          "Compose the catalog from multiple independent sources with deterministic merge and explicit precedence.",
          "Use your own Git repos as sources.",
        ],
        diagram: "layers",
      },
      {
        number: "05",
        title: "Security and access control",
        body: [
          "Skills and other artifacts are signed at ingest and verified on every sync. Declare who can see what with RBAC. Audit logs.",
        ],
        diagram: "security",
      },
      {
        number: "06",
        title: "Observability",
        body: [
          "Every artifact load and every search is an audit event. Stream the events to your log pipeline to see which artifacts agents load and how often.",
        ],
        diagram: "observability",
      },
    ],
  },
  deployment: {
    heading: "Deployment modes",
    cards: [
      {
        icon: "folder",
        title: "Local",
        badge: "No server needed",
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
        badge: null,
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
        badge: null,
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
          "Postgres tenant isolation",
          "High availability",
        ],
      },
    ],
    callout: {
      glyph: "→",
      text: "Skills and other artifacts are independent of Podium's deployment model.",
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
      {
        icon: "scroll",
        name: "Audit log",
        builtIn: "Hash-chained file",
        alternatives: ["stdout", "HTTP receivers", "OpenTelemetry Collector"],
        note: "List several destinations to write one hash-chained stream to each.",
      },
      {
        icon: "pulse",
        name: "Tracing",
        builtIn: "None",
        alternatives: ["OTLP/HTTP", "stdout"],
        note: "Spans carry W3C Trace Context, and URL query strings are removed before export.",
      },
      {
        icon: "bell",
        name: "Notifications",
        builtIn: "None",
        alternatives: ["Webhook", "Email (SMTP)", "Log"],
        note: "Delivers layer ingest-failure alerts. Several providers can run together.",
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

/** A transcript command line, set in the heavier weight. */
function command(prompt: boolean, ...spans: TerminalSpan[]): TerminalLine {
  return { kind: "command", prompt, spans };
}

/** A transcript line the command printed. */
function output(...spans: TerminalSpan[]): TerminalLine {
  return { kind: "output", spans };
}

function ink(text: string): TerminalSpan {
  return { text, tone: "ink" };
}

function arg(text: string): TerminalSpan {
  return { text, tone: "arg" };
}

function out(text: string): TerminalSpan {
  return { text, tone: "out" };
}
