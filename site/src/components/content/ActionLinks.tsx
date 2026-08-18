import type { ActionLink } from "../../build/types";

/**
 * Renders the frontmatter action links. The first is the primary action and the
 * rest are outlined, which is the order the list is written in.
 */
export function ActionLinks(props: { actions: ActionLink[] }) {
  if (props.actions.length === 0) return null;
  return (
    <div className="action-links">
      {props.actions.map((action) => (
        <a
          key={action.href}
          className={`action-link action-link-${action.variant}`}
          href={action.href}
          {...(action.external ? { rel: "noreferrer", target: "_blank" } : {})}
        >
          {action.label}
        </a>
      ))}
    </div>
  );
}
