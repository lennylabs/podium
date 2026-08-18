/*
 * Landing page copy, held as typed data so the page component contains layout
 * and no prose. Every string the landing design shows lives here.
 *
 * Link targets are stored as routes, not as finished URLs. The page component
 * resolves a "doc" target against SiteConfig.basePath and a "repo" target
 * against SiteConfig.repoUrl, so a base path change lands in one place.
 */

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

export type ActionVariant = "primary" | "outline" | "ghost" | "accent";

export type Action = {
  label: string;
  target: LinkTarget;
  variant: ActionVariant;
};

/** A run inside an output line. Muted runs carry annotations such as `[team]`. */
export type TerminalSegment = {
  text: string;
  tone: "plain" | "muted";
};

/**
 * One line of the hero transcript.
 *
 * A "command" line renders behind a prompt glyph. A "continuation" line carries
 * its own leading whitespace and renders without a prompt. A "path-highlight"
 * line is a materialized file path, tinted with the accent. A "cursor" line is
 * the trailing prompt and the blinking block.
 */
export type TerminalLine =
  | { kind: "command"; text: string }
  | { kind: "continuation"; text: string }
  | { kind: "output"; segments: TerminalSegment[] }
  | { kind: "path-highlight"; indent: number; text: string }
  | { kind: "blank" }
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
  /** Mono badge naming the component the feature requires, or null. */
  badge: string | null;
  /** True for the single accent-filled card. */
  accent: boolean;
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
    /** Mono note beside the heading. */
    note: string;
    cards: FeatureCard[];
  };
  cta: {
    heading: string;
    body: string;
    action: Action;
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
        label: "Reference",
        target: { kind: "doc", route: "/reference/index.html" },
        emphasized: false,
      },
      {
        label: "Deployment",
        target: { kind: "doc", route: "/deployment/index.html" },
        emphasized: false,
      },
      {
        // The RFC tree is published as raw markdown with no index page, so the
        // link goes to the directory in the repository, where each file renders.
        label: "RFCs",
        target: { kind: "repo", path: "/tree/main/docs/rfc" },
        emphasized: false,
      },
      {
        label: "GitHub",
        target: { kind: "repo", path: "" },
        emphasized: false,
      },
    ],
    versionLabel: "Podium version",
  },
  hero: {
    status: {
      text: "0.1.x — early release",
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
      directory: "~/projects/checkout-svc",
      prompt: "$",
      caption:
        "Terminal transcript. The podium init command points a workspace at a catalog and a harness, and podium sync materializes three artifacts into the workspace.",
      lines: [
        { kind: "command", text: "podium init --registry ~/podium-artifacts/ \\" },
        { kind: "continuation", text: "    --harness claude-code" },
        { kind: "command", text: "podium sync" },
        { kind: "blank" },
        { kind: "output", segments: [{ text: "adapter: claude-code", tone: "plain" }] },
        {
          kind: "output",
          segments: [{ text: "target:  /Users/alice/checkout-svc", tone: "plain" }],
        },
        { kind: "output", segments: [{ text: "artifacts:", tone: "plain" }] },
        {
          kind: "output",
          segments: [
            { text: "  - platform/deploy/rollback     ", tone: "plain" },
            { text: "[team]", tone: "muted" },
          ],
        },
        {
          kind: "path-highlight",
          indent: 6,
          text: ".claude/skills/rollback/SKILL.md",
        },
        {
          kind: "output",
          segments: [
            { text: "  - platform/pg/query-review     ", tone: "plain" },
            { text: "[team]", tone: "muted" },
          ],
        },
        {
          kind: "path-highlight",
          indent: 6,
          text: ".claude/skills/query-review/SKILL.md",
        },
        {
          kind: "output",
          segments: [
            { text: "  - personal/hello/greet         ", tone: "plain" },
            { text: "[personal]", tone: "muted" },
          ],
        },
        {
          kind: "path-highlight",
          indent: 6,
          text: ".claude/skills/greet/SKILL.md",
        },
        { kind: "blank" },
        {
          kind: "output",
          segments: [{ text: "3 artifacts materialized in 41ms", tone: "plain" }],
        },
        { kind: "cursor" },
      ],
    },
  },
  adapters: {
    label: "ADAPTERS",
    names: [
      "Claude Code",
      "Claude Desktop",
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
    heading: "What you get",
    note: "SERVER-ONLY FEATURES MARKED",
    cards: [
      {
        number: "01",
        title: "Cross-harness delivery",
        body: "Pluggable adapters translate canonical artifacts into whichever runtime the developer actually uses.",
        badge: null,
        accent: false,
      },
      {
        number: "02",
        title: "Domains and subdomains",
        body: "Keep artifacts in folders and subfolders, where each folder defines a domain.",
        badge: null,
        accent: false,
      },
      {
        number: "03",
        title: "Selective materialization",
        body: "Sync a subset of the catalog into a workspace. Define profiles to switch between scopes.",
        badge: null,
        accent: false,
      },
      {
        number: "04",
        title: "Layered composition",
        body: "Compose the catalog from multiple sources with deterministic merge and explicit precedence.",
        badge: "SERVER",
        accent: true,
      },
      {
        number: "05",
        title: "Per-layer visibility",
        body: "Declare who can see what: public, org-wide, scoped to OIDC groups, or specific users.",
        badge: "SERVER",
        accent: false,
      },
      {
        number: "06",
        title: "Progressive discovery",
        body: "Agents traverse domains and search artifacts, materializing files lazily as they load.",
        badge: "MCP / SDK",
        accent: false,
      },
    ],
  },
  cta: {
    heading: "Two ways to run it",
    body: "Filesystem catalog for solo work, prototypes, CI, and Git-shared catalogs. Registry server when you need runtime discovery, identity-aware visibility, and audit.",
    action: {
      label: "Compare deployment setups",
      target: { kind: "doc", route: "/deployment/index.html" },
      variant: "accent",
    },
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
