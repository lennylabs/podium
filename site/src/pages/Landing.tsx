import { Fragment } from "react";
import type * as React from "react";

import {
  deploymentIcons,
  featureDiagrams,
  integrationIcons,
} from "../components/content/FeatureDiagrams";
import { Lockup } from "../components/layout/Lockup";
import type { SiteConfig } from "../build/types";
import type {
  Action,
  DeploymentCard,
  FeatureCard,
  IntegrationRow,
  LinkTarget,
  NavLink,
  TerminalLine,
} from "../content/landing";
import { landing } from "../content/landing";

/**
 * The landing page body: top bar, hero, adapter strip, feature grid, deployment
 * grid, integrations table, and footer. The document shell is written elsewhere,
 * so this component renders a fragment and no html, head, or body element.
 *
 * The page carries no client-side state. The copy control is plain markup
 * annotated with data attributes, and the hydration script binds the clipboard
 * behavior to it.
 */
export function Landing(props: { config: SiteConfig }): React.ReactElement {
  const { config } = props;
  const { nav, hero, adapters, features, deployment, integrations, footer } = landing;

  return (
    <>
      <header className="l-topbar">
        <div className="l-topbar-inner">
          <a className="l-brand" href={resolve(config, nav.home)}>
            <Lockup className="l-lockup" label={nav.wordmark} />
          </a>
          <nav className="l-nav" aria-label="Site">
            {nav.links.map((link: NavLink) => (
              <a
                key={link.label}
                className={
                  link.emphasized ? "l-nav-link l-nav-link--strong" : "l-nav-link"
                }
                href={resolve(config, link.target)}
                {...externalAttrs(link.target)}
              >
                {link.label}
                {link.target.kind === "repo" ? (
                  <span className="l-nav-arrow" aria-hidden="true">
                    ↗
                  </span>
                ) : null}
              </a>
            ))}
          </nav>
          <span className="l-version">
            <span className="l-sr-only">{nav.versionLabel}: </span>
            {nav.versionPrefix}
            {config.version}
          </span>
        </div>
      </header>

      <section className="l-hero" aria-labelledby="l-headline">
        <div className="l-hero-inner">
          <div className="l-hero-copy">
            <p className="l-status">
              <span className="l-status-dot" aria-hidden="true" />
              {`${series(config.version)} — ${hero.status.qualifier}`}
            </p>

            <h1 className="l-headline" id="l-headline">
              {hero.headline.map((line, index) => (
                <Fragment key={line.text}>
                  {index > 0 ? <br /> : null}
                  {line.marked ? (
                    <span className="l-mark">{line.text}</span>
                  ) : (
                    line.text
                  )}
                </Fragment>
              ))}
            </h1>

            <p className="l-subtitle">
              {hero.subtitle.map((run) =>
                run.marked ? (
                  <span className="l-mark-soft" key={run.text}>
                    {run.text}
                  </span>
                ) : (
                  <Fragment key={run.text}>{run.text}</Fragment>
                ),
              )}
            </p>

            <div className="l-install">
              <span className="l-install-prompt" aria-hidden="true">
                {hero.install.prompt}
              </span>
              <code className="l-install-command" id="l-install-command">
                {hero.install.command}
              </code>
              <span className="l-install-divider" aria-hidden="true" />
              <button
                className="l-copy"
                type="button"
                data-copy-target="#l-install-command"
                data-copy-label={hero.install.copyLabel}
                data-copy-done-label={hero.install.copiedLabel}
              >
                {hero.install.copyLabel}
              </button>
            </div>

            <div className="l-actions">
              {hero.actions.map((action: Action) => (
                <a
                  key={action.label}
                  className={`l-btn l-btn--${action.variant}`}
                  href={resolve(config, action.target)}
                  {...externalAttrs(action.target)}
                >
                  {action.label}
                </a>
              ))}
            </div>
          </div>

          <figure className="l-terminal">
            <div className="l-terminal-card">
              <div className="l-terminal-bar">
                <span className="l-terminal-dot l-terminal-dot--live" aria-hidden="true" />
                <span className="l-terminal-dot" aria-hidden="true" />
                <span className="l-terminal-dot" aria-hidden="true" />
                <span className="l-terminal-dir">{hero.terminal.directory}</span>
              </div>
              <div className="l-term">
                {hero.terminal.lines.map((line, index) => (
                  <TranscriptLine key={index} line={line} prompt={hero.terminal.prompt} />
                ))}
              </div>
            </div>
            <figcaption className="l-sr-only">{hero.terminal.caption}</figcaption>
          </figure>
        </div>
      </section>

      <section className="l-adapters" aria-label={adapters.label}>
        <div className="l-adapters-inner">
          <span className="l-adapters-label">{adapters.label}</span>
          {adapters.names.map((name) => (
            <span className="l-adapter" key={name}>
              {name}
            </span>
          ))}
          <span className="l-adapter l-adapter--custom">{adapters.custom}</span>
        </div>
      </section>

      <section className="l-features" aria-labelledby="l-features-heading">
        <h2 className="l-heading" id="l-features-heading">
          {features.heading}
        </h2>
        <div className="l-grid">
          {features.cards.map((card: FeatureCard) => {
            const Diagram = featureDiagrams[card.diagram];

            return (
              <article className="l-card" key={card.number}>
                <div className="l-card-art">
                  <Diagram />
                </div>
                <div className="l-card-text">
                  <p className="l-card-number">{card.number}</p>
                  <h3 className="l-card-title">{card.title}</h3>
                  <p className="l-card-body">{card.body}</p>
                </div>
              </article>
            );
          })}
        </div>
      </section>

      <section className="l-deploy" aria-labelledby="l-deploy-heading">
        <h2 className="l-heading" id="l-deploy-heading">
          {deployment.heading}
        </h2>
        <div className="l-deploy-grid">
          {deployment.cards.map((card: DeploymentCard) => {
            const Icon = deploymentIcons[card.icon];

            return (
              <article className="l-deploy-card" key={card.title}>
                <div className="l-deploy-head">
                  <Icon />
                  <h3 className="l-deploy-title">{card.title}</h3>
                </div>
                <div className="l-deploy-rule" />
                <dl className="l-deploy-facts">
                  {card.facts.map((fact) => (
                    <div className="l-deploy-fact" key={fact.label}>
                      <dt className="l-deploy-label">{fact.label}</dt>
                      <dd className="l-deploy-value">{fact.value}</dd>
                    </div>
                  ))}
                </dl>
                <div className="l-deploy-plus">
                  {card.plusLabel === null ? null : (
                    <p className="l-deploy-plus-label">{card.plusLabel}</p>
                  )}
                  <ul className="l-deploy-list">
                    {card.plus.map((item) => (
                      <li className="l-deploy-item" key={item}>
                        <span className="l-deploy-dot" aria-hidden="true" />
                        {item}
                      </li>
                    ))}
                  </ul>
                </div>
              </article>
            );
          })}
        </div>
        <p className="l-callout">
          <span className="l-callout-glyph" aria-hidden="true">
            {deployment.callout.glyph}
          </span>
          <span className="l-callout-text">{deployment.callout.text}</span>
        </p>
      </section>

      <section className="l-int" aria-labelledby="l-int-heading">
        <h2 className="l-heading" id="l-int-heading">
          {integrations.heading}
        </h2>
        <div className="l-int-scroll">
          <table className="l-int-table">
            <thead>
              <tr>
                <th className="l-int-head" scope="col">
                  <span className="l-sr-only">{integrations.columns.subject}</span>
                </th>
                <th className="l-int-head" scope="col">
                  {integrations.columns.builtIn}
                </th>
                <th className="l-int-head" scope="col">
                  {integrations.columns.alternatives}
                </th>
              </tr>
            </thead>
            <tbody>
              {integrations.rows.map((row: IntegrationRow) => {
                const Icon = integrationIcons[row.icon];

                return (
                  <tr className="l-int-row" key={row.name}>
                    <th className="l-int-name" scope="row">
                      <span className="l-int-cell">
                        <Icon />
                        {row.name}
                      </span>
                    </th>
                    <td className="l-int-builtin">{row.builtIn}</td>
                    <td className="l-int-alts">
                      <ul className="l-int-pills">
                        {row.alternatives.map((alternative) => (
                          <li className="l-int-pill" key={alternative}>
                            {alternative}
                          </li>
                        ))}
                      </ul>
                      {row.note === null ? null : (
                        <p className="l-int-note">{row.note}</p>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        <p className="l-int-foot">{integrations.note}</p>
      </section>

      <footer className="l-footer">
        <div className="l-footer-inner">
          <span className="l-footer-legal">{footer.license}</span>
          <span className="l-footer-sep" aria-hidden="true">
            {footer.separator}
          </span>
          <a
            className="l-footer-link"
            href={resolve(config, footer.repo.target)}
            {...externalAttrs(footer.repo.target)}
          >
            {footer.repo.label}
          </a>
          <nav className="l-footer-links" aria-label="Project">
            {footer.links.map((link, index) => (
              <Fragment key={link.label}>
                {index > 0 ? (
                  <span className="l-footer-sep" aria-hidden="true">
                    {footer.separator}
                  </span>
                ) : null}
                <a
                  className="l-footer-link"
                  href={resolve(config, link.target)}
                  {...externalAttrs(link.target)}
                >
                  {link.label}
                </a>
              </Fragment>
            ))}
          </nav>
        </div>
      </footer>
    </>
  );
}

/** Turns a content link target into an href for this build. */
/**
 * The release series a reader pins against, from the build's own version.
 *
 * "0.2.1" becomes "0.2.x". A version carrying a prerelease suffix keeps the
 * same series, since the suffix sits on the patch element.
 */
function series(version: string): string {
  const [major, minor] = version.split(".");
  return major === undefined || minor === undefined ? version : `${major}.${minor}.x`;
}

function resolve(config: SiteConfig, target: LinkTarget): string {
  switch (target.kind) {
    case "doc":
      return `${config.basePath}${target.route}`;
    case "repo":
      return `${config.repoUrl}${target.path}`;
  }
}

/** Attributes a link that leaves the documentation site carries. */
function externalAttrs(target: LinkTarget): { rel?: string; target?: string } {
  if (target.kind === "repo") {
    return { rel: "noreferrer", target: "_blank" };
  }
  return {};
}

/**
 * One transcript line, as its own block element. The two spaces in front of a
 * materialized path are the indent the design calls for, and the line preserves
 * them because .l-term-line sets white-space: pre.
 */
function TranscriptLine(props: { line: TerminalLine; prompt: string }): React.ReactElement {
  const { line, prompt } = props;

  switch (line.kind) {
    case "command":
      return (
        <div className="l-term-line">
          <span className="l-term-prompt">{prompt}</span> {line.command}{" "}
          <span className="l-term-flag">{line.flag}</span>
        </div>
      );
    case "path":
      return (
        <div className="l-term-line">
          {"  "}
          <span className="l-term-out">{line.text}</span>
        </div>
      );
    case "gap":
      return <div className="l-term-gap" />;
    case "cursor":
      return (
        <div className="l-term-line">
          <span className="l-term-prompt">{prompt}</span>{" "}
          <span className="l-term-cursor" aria-hidden="true" />
        </div>
      );
  }
}
