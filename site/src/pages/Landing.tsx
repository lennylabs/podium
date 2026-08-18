import { Fragment } from "react";
import type * as React from "react";

import { Lockup } from "../components/layout/Lockup";
import type { SiteConfig } from "../build/types";
import type {
  Action,
  FeatureCard,
  LinkTarget,
  NavLink,
  TerminalLine,
} from "../content/landing";
import { landing } from "../content/landing";

/**
 * The landing page body: top bar, hero, adapter strip, feature grid, call to
 * action band, and footer. The document shell is written elsewhere, so this
 * component renders a fragment and no html, head, or body element.
 *
 * The page carries no client-side state. The copy control is plain markup
 * annotated with data attributes, and the hydration script binds the clipboard
 * behavior to it.
 */
export function Landing(props: { config: SiteConfig }): React.ReactElement {
  const { config } = props;
  const { nav, hero, adapters, features, cta, footer } = landing;

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
            {config.version}
          </span>
        </div>
      </header>

      <section className="l-hero" aria-labelledby="l-headline">
        <div className="l-hero-inner">
          <div className="l-hero-copy">
            <p className="l-status">
              <span className="l-status-dot" aria-hidden="true" />
              {hero.status.text}
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
              <pre className="l-term">
                <code>
                  {hero.terminal.lines.map((line, index) => (
                    <Fragment key={index}>
                      {index > 0 ? "\n" : null}
                      {renderTerminalLine(line, hero.terminal.prompt)}
                    </Fragment>
                  ))}
                </code>
              </pre>
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
        <div className="l-features-head">
          <h2 className="l-features-heading" id="l-features-heading">
            {features.heading}
          </h2>
          <span className="l-features-note">{features.note}</span>
        </div>
        <div className="l-grid">
          {features.cards.map((card: FeatureCard) => (
            <article
              className={card.accent ? "l-card l-card--accent" : "l-card"}
              key={card.number}
            >
              <div className="l-card-meta">
                <span className="l-card-number">{card.number}</span>
                {card.badge === null ? null : (
                  <span className="l-badge">{card.badge}</span>
                )}
              </div>
              <h3 className="l-card-title">{card.title}</h3>
              <p className="l-card-body">{card.body}</p>
            </article>
          ))}
        </div>
      </section>

      <section className="l-cta" aria-labelledby="l-cta-heading">
        <div className="l-cta-inner">
          <div className="l-cta-copy">
            <h2 className="l-cta-heading" id="l-cta-heading">
              {cta.heading}
            </h2>
            <p className="l-cta-body">{cta.body}</p>
          </div>
          <a
            className={`l-btn l-btn--${cta.action.variant}`}
            href={resolve(config, cta.action.target)}
            {...externalAttrs(cta.action.target)}
          >
            {cta.action.label}
          </a>
        </div>
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

/** Renders one transcript line. Callers insert the newline between lines. */
function renderTerminalLine(line: TerminalLine, prompt: string): React.ReactNode {
  switch (line.kind) {
    case "command":
      return (
        <>
          <span className="l-term-prompt">{prompt}</span>
          {" "}
          {line.text}
        </>
      );
    case "continuation":
      return line.text;
    case "output":
      return (
        <>
          {line.segments.map((segment, index) =>
            segment.tone === "muted" ? (
              <span className="l-term-muted" key={index}>
                {segment.text}
              </span>
            ) : (
              <Fragment key={index}>{segment.text}</Fragment>
            ),
          )}
        </>
      );
    case "path-highlight":
      return (
        <>
          {" ".repeat(line.indent)}
          <span className="l-term-path">{line.text}</span>
        </>
      );
    case "blank":
      return null;
    case "cursor":
      return (
        <>
          <span className="l-term-prompt">{prompt}</span>
          {" "}
          <span className="l-term-cursor" aria-hidden="true" />
        </>
      );
  }
}
