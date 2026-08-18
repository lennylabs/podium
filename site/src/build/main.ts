import { existsSync, readFileSync, rmSync } from "node:fs";
import { join, resolve } from "node:path";

import { createElement } from "react";

import { registry } from "../components/islands/registry";
import { validateRegistry } from "../components/islands/props";
import { Doc } from "../pages/Doc";
import { Landing } from "../pages/Landing";
import { NotFound } from "../pages/NotFound";
import { copyStatics, emitFonts, emitStylesheet, writeFile } from "./assets";
import { runChecks } from "./check";
import { SITE_DIR, loadConfig } from "./config";
import { buildCorpus } from "./content/pipeline";
import { disposeHighlighter } from "./content/highlight";
import { buildNav, flattenNav, neighbours } from "./nav";
import { renderBody, renderDocument, resolveIslandComponents } from "./render";
import { buildSearchIndex } from "./search";
import { BuildError, type BuildDiagnostic, type NavNode, type PageModel, type SiteConfig } from "./types";

type Command = "build" | "check" | "dev";

async function main(): Promise<void> {
  const command = (process.argv[2] ?? "build") as Command;
  const config = loadConfig();

  switch (command) {
    case "build":
      await runBuild(config);
      break;
    case "check":
      await runCheck(config);
      break;
    case "dev":
      await runDev(config);
      break;
    default:
      process.stderr.write(`unknown command "${command}". Use build, check, or dev.\n`);
      process.exitCode = 1;
  }
}

/** Everything both build and check need, with every diagnostic collected. */
async function assemble(config: SiteConfig): Promise<{
  pages: PageModel[];
  nav: NavNode[];
  diagnostics: BuildDiagnostic[];
  index: Awaited<ReturnType<typeof buildCorpus>>["index"];
  statics: Awaited<ReturnType<typeof buildCorpus>>["statics"];
  search: { serialized: string; documentCount: number };
}> {
  const diagnostics: BuildDiagnostic[] = [...validateRegistry(registry)];

  const corpus = await buildCorpus(config, registry);
  diagnostics.push(...corpus.diagnostics);

  const nav = buildNav(corpus.pages);

  const sectionTitles = new Map<string, string>();
  for (const page of corpus.pages) sectionTitles.set(page.route, page.title);

  const search = buildSearchIndex(corpus.pages, sectionTitles);

  return {
    pages: corpus.pages,
    nav,
    diagnostics,
    index: corpus.index,
    statics: corpus.statics,
    search,
  };
}

function report(diagnostics: BuildDiagnostic[]): void {
  if (diagnostics.length === 0) return;
  throw new BuildError(diagnostics);
}

async function runCheck(config: SiteConfig): Promise<void> {
  const assembled = await assemble(config);
  const diagnostics = [
    ...assembled.diagnostics,
    ...runChecks({
      config,
      pages: assembled.pages,
      nav: assembled.nav,
      index: assembled.index,
      searchIndexBytes: Buffer.byteLength(assembled.search.serialized),
    }),
  ];
  report(diagnostics);
  process.stdout.write(
    `checked ${assembled.pages.length} pages, ${assembled.index.routes.size} routes, no problems\n`,
  );
}

async function runBuild(config: SiteConfig): Promise<void> {
  const assembled = await assemble(config);
  const diagnostics = [
    ...assembled.diagnostics,
    ...runChecks({
      config,
      pages: assembled.pages,
      nav: assembled.nav,
      index: assembled.index,
      searchIndexBytes: Buffer.byteLength(assembled.search.serialized),
    }),
  ];
  report(diagnostics);

  rmSync(config.outDir, { recursive: true, force: true });

  const fontCss = emitFonts(config);
  const stylesheet = emitStylesheet(config, fontCss);
  const scriptSrc = await buildClientBundle(config);

  for (const page of assembled.pages) {
    const components = await resolveIslandComponents(registry, page.islands);
    const { prev, next } = neighbours(assembled.nav, page.route);
    const body = renderBody(page, components);

    const html = renderDocument({
      config,
      title: `${page.title} · Podium`,
      description: page.description,
      route: page.route,
      bodyClass: "docs",
      scriptSrc,
      stylesheets: [stylesheet],
      hydratable: page.islands.some((island) => island.mount === "hydrate"),
      children: createElement(Doc, {
        config,
        page,
        nav: assembled.nav,
        prev,
        next,
        children: body,
      }),
    });

    writeFile(join(config.outDir, page.route), html);
  }

  writeFile(
    join(config.outDir, "index.html"),
    renderDocument({
      config,
      title: "Podium · One catalog. Every harness.",
      description:
        "Podium is a catalog for reusable AI agent artifacts, with tooling that translates them into harness-specific formats.",
      route: "/index.html",
      bodyClass: "landing",
      scriptSrc,
      stylesheets: [stylesheet],
      hydratable: false,
      children: createElement(Landing, { config }),
    }),
  );

  writeFile(
    join(config.outDir, "404.html"),
    renderDocument({
      config,
      title: "Not found · Podium",
      description: "The page you asked for does not exist.",
      route: "/404.html",
      bodyClass: "docs",
      scriptSrc,
      stylesheets: [stylesheet],
      hydratable: false,
      children: createElement(NotFound, { config, nav: assembled.nav }),
    }),
  );

  writeFile(join(config.outDir, "search-index.json"), assembled.search.serialized);
  copyStatics(config, assembled.statics);
  writeFile(join(config.outDir, ".nojekyll"), "");

  disposeHighlighter();

  process.stdout.write(
    `built ${assembled.pages.length} pages and ${assembled.statics.length} static files into ${config.outDir}\n`,
  );
}

/**
 * Builds the browser bundle and returns the URL of its entry.
 *
 * Vite writes a manifest so the emitted filename, which carries a content hash,
 * can be looked up rather than guessed.
 */
async function buildClientBundle(config: SiteConfig): Promise<string> {
  const { build } = await import("vite");
  await build({
    root: SITE_DIR,
    logLevel: "warn",
    build: {
      outDir: join(config.outDir, "assets/site"),
      emptyOutDir: true,
      manifest: true,
      rollupOptions: {
        input: resolve(SITE_DIR, "src/client/entry.ts"),
      },
    },
  });

  const manifestPath = join(config.outDir, "assets/site/.vite/manifest.json");
  if (!existsSync(manifestPath)) {
    throw new Error(`the client build wrote no manifest at ${manifestPath}`);
  }
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8")) as Record<
    string,
    { file: string; isEntry?: boolean }
  >;
  const entry = Object.values(manifest).find((chunk) => chunk.isEntry);
  if (entry === undefined) {
    throw new Error("the client build manifest names no entry chunk");
  }
  return `${config.basePath}/assets/site/${entry.file}`;
}

/** Rebuilds on change and serves the output, for local authoring. */
async function runDev(config: SiteConfig): Promise<void> {
  const { watch } = await import("node:fs");
  const { createServer } = await import("node:http");
  const { extname } = await import("node:path");

  const rebuild = async (): Promise<void> => {
    try {
      await runBuild(config);
    } catch (error) {
      process.stderr.write(`${(error as Error).message}\n`);
    }
  };

  await rebuild();

  let pending: NodeJS.Timeout | null = null;
  const schedule = (): void => {
    if (pending !== null) clearTimeout(pending);
    pending = setTimeout(() => void rebuild(), 150);
  };

  watch(config.docsDir, { recursive: true }, schedule);
  watch(resolve(SITE_DIR, "src"), { recursive: true }, schedule);

  const types: Record<string, string> = {
    ".html": "text/html; charset=utf-8",
    ".css": "text/css; charset=utf-8",
    ".js": "text/javascript; charset=utf-8",
    ".json": "application/json; charset=utf-8",
    ".svg": "image/svg+xml",
    ".woff2": "font/woff2",
    ".md": "text/markdown; charset=utf-8",
  };

  const port = Number(process.env.PORT ?? 4321);
  createServer((request, response) => {
    const url = new URL(request.url ?? "/", `http://localhost:${port}`);
    let path = decodeURIComponent(url.pathname);
    if (config.basePath !== "" && path.startsWith(config.basePath)) {
      path = path.slice(config.basePath.length);
    }
    if (path === "" || path.endsWith("/")) path = `${path}index.html`;

    const file = join(config.outDir, path);
    if (!file.startsWith(config.outDir) || !existsSync(file)) {
      response.writeHead(404, { "content-type": "text/plain" });
      response.end("not found");
      return;
    }
    response.writeHead(200, {
      "content-type": types[extname(file)] ?? "application/octet-stream",
    });
    response.end(readFileSync(file));
  }).listen(port, () => {
    process.stdout.write(
      `serving ${config.outDir} at http://localhost:${port}${config.basePath}/\n`,
    );
  });
}

main().catch((error: unknown) => {
  process.stderr.write(`${(error as Error).message}\n`);
  process.exitCode = 1;
});
