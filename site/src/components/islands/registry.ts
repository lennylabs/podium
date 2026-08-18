import type { TabPanelData } from "../content/tab-data";
import { defineIsland, type IslandRegistry } from "./props";

/**
 * Every directive name the corpus may use, mapped to what it renders and what
 * it accepts. The directive name is the key: `::::tabs` resolves to `tabs`, so
 * one lookup produces both the component and its prop schema.
 *
 * The entries here are structural. They arrange content that is already on the
 * page rather than adding behaviour of their own, so a reader with no
 * JavaScript loses nothing. Interactive entries are added alongside these.
 */
export const registry: IslandRegistry = {
  tabs: defineIsland<Record<string, never>, { panels: TabPanelData[] }>({
    props: {},
    children: { kind: "directives", name: "tab", min: 2 },
    fallback: { from: "children" },
    load: async () => ({ default: (await import("../content/Tabs")).Tabs }),
  }),

  tab: defineIsland<{ label: string }>({
    props: {
      label: { kind: "string", required: true },
    },
    children: { kind: "markdown" },
    fallback: { from: "children" },
  }),

  diagram: defineIsland<{ name: string; alt: string }>({
    props: {
      // Resolved as the stem of a checked-in diagram, so `name="sync-watch"`
      // means docs/assets/diagrams/sync-watch.svg and a missing file fails.
      name: {
        kind: "asset",
        required: true,
        within: "assets/diagrams",
        extension: ".svg",
      },
      // Required because the static SVG is what most readers see, and an image
      // without alt text fails the accessibility rule.
      alt: { kind: "string", required: true },
    },
    children: { kind: "none" },
    fallback: { from: "prop", name: "name" },
    load: async () => ({
      default: (await import("../content/DiagramIsland")).DiagramIsland,
    }),
  }),
};
