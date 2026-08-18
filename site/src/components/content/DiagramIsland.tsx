import { diagramVariants } from "../diagrams";
import { lazy, Suspense, type ReactElement } from "react";

/**
 * Renders an animated diagram variant when one is registered for the stem.
 *
 * The build only emits this island when a variant exists, so the fallback path
 * here covers a variant removed between builds rather than the common case.
 * The checked-in SVG stays the content either way.
 */
export function DiagramIsland(props: { name: string; alt: string }): ReactElement {
  const stem = props.name.replace(/^.*\//, "").replace(/\.svg$/, "");
  const loader = diagramVariants[stem];
  if (loader === undefined) {
    return <img className="diagram" src={props.name} alt={props.alt} />;
  }
  const Variant = lazy(loader);
  return (
    <Suspense fallback={<img className="diagram" src={props.name} alt={props.alt} />}>
      <Variant name={props.name} alt={props.alt} />
    </Suspense>
  );
}
