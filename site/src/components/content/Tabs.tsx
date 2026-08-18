import { useEffect, useId, useState, type ReactElement } from "react";

import type { TabPanelData } from "./tab-data";

export type { TabPanelData };

/**
 * Arranges markdown sections as a tab strip.
 *
 * Panels arrive as rendered markup rather than as React children, which is what
 * lets the server and the browser render this component from identical data.
 * The build renders each panel once, the markup travels in the page, and the
 * client reads it back out of the DOM before hydrating.
 *
 * The first client render reproduces the server's output exactly, with every
 * panel visible, and the interactive arrangement is applied after mounting. A
 * reader without JavaScript therefore sees every panel in source order, and
 * hydration has matching markup to attach to.
 */
export function Tabs(props: { panels: TabPanelData[] }): ReactElement {
  const { panels } = props;
  const [mounted, setMounted] = useState(false);
  const [active, setActive] = useState(0);
  const groupId = useId();

  useEffect(() => {
    setMounted(true);
    // The chosen channel is remembered across pages, so a reader who installs
    // with one tool is not asked again on the next page.
    try {
      const stored = window.localStorage.getItem("podium-tab-choice");
      if (stored === null) return;
      const match = panels.findIndex((panel) => panel.label === stored);
      if (match >= 0) setActive(match);
    } catch {
      // A blocked storage read leaves the first panel selected.
    }
  }, [panels]);

  const choose = (index: number, label: string): void => {
    setActive(index);
    try {
      window.localStorage.setItem("podium-tab-choice", label);
    } catch {
      // A blocked storage write must not break tab switching.
    }
  };

  return (
    <div className="tabs" data-tabs data-mounted={mounted ? "true" : "false"}>
      <div className="tabs-strip" role={mounted ? "tablist" : undefined}>
        {panels.map((panel, index) => (
          <button
            key={panel.label}
            type="button"
            role={mounted ? "tab" : undefined}
            id={`${groupId}-tab-${index}`}
            aria-selected={mounted ? index === active : undefined}
            aria-controls={`${groupId}-panel-${index}`}
            className={
              mounted && index === active ? "tabs-tab tabs-tab-active" : "tabs-tab"
            }
            onClick={() => choose(index, panel.label)}
          >
            {panel.label}
          </button>
        ))}
      </div>
      {panels.map((panel, index) => (
        <div
          key={panel.label}
          role={mounted ? "tabpanel" : undefined}
          id={`${groupId}-panel-${index}`}
          aria-labelledby={`${groupId}-tab-${index}`}
          hidden={mounted && index !== active}
          className="tabs-panel"
          data-tab-label={panel.label}
          dangerouslySetInnerHTML={{ __html: panel.html }}
        />
      ))}
    </div>
  );
}
