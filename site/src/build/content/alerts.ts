import type { Blockquote, Root } from "mdast";
import type { Plugin } from "unified";
import { visit } from "unist-util-visit";

/** The alert types GitHub renders natively. */
export const ALERT_TYPES = [
  "note",
  "tip",
  "important",
  "warning",
  "caution",
] as const;

export type AlertType = (typeof ALERT_TYPES)[number];

const MARKER = /^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*/;

/**
 * Turns a GitHub alert blockquote into a callout element.
 *
 *   > [!NOTE]
 *   > Text.
 *
 * The marker is stripped and the blockquote is renamed, so the same source
 * renders as an alert on GitHub and as a callout here. A blockquote with no
 * marker is left as a blockquote.
 *
 * remark-rehype has no handler for a renamed node, so the rename must go
 * through data.hName: without it the node would fall back to a plain
 * blockquote and the callout treatment would silently disappear.
 */
export const remarkAlerts: Plugin<[], Root> = () => (tree: Root) => {
  visit(tree, "blockquote", (node: Blockquote) => {
    const first = node.children[0];
    if (!first || first.type !== "paragraph") return;

    const firstChild = first.children[0];
    if (!firstChild || firstChild.type !== "text") return;

    const match = firstChild.value.match(MARKER);
    if (!match || match[1] === undefined) return;

    const type = match[1].toLowerCase() as AlertType;
    firstChild.value = firstChild.value.slice(match[0].length);

    // A marker on its own line leaves an empty leading paragraph behind.
    if (firstChild.value.trim() === "" && first.children.length === 1) {
      node.children.shift();
    } else {
      firstChild.value = firstChild.value.replace(/^\n/, "");
    }

    const data = node.data ?? (node.data = {});
    data.hName = "podium-callout";
    data.hProperties = { type };
  });
};
