// The similarity case set below the surfaces: the grouping of the §7.3.1
// overlap report into entries and peer rows, the two score formats, and the
// presentational components the two similarity screens are built from. The
// screens themselves are driven through the shell in surfaces.test.tsx.

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { OverlapDiagnostic, OverlapReport } from "./api";
import { overlapReport } from "./api";
import { ClusterList, strongestFigure } from "./components/ClusterList";
import { MessageBox } from "./components/MessageBox";
import { DescriptionCompare, OverlapTable } from "./components/OverlapTable";
import { RangeSlider } from "./components/RangeSlider";
import { ScoreBar } from "./components/ScoreBar";
import {
  clampThreshold,
  clusterEntries,
  countWord,
  formatSimilarity,
  formatStrongest,
  formatThreshold,
  formatWordOverlap,
  overlapEntry,
  thinDiagnostic,
} from "./similarity";
import "./index.css";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const a = { artifact_id: "ops/a", description: "Alpha.", type: "skill", layer: "ops" };
const b = { artifact_id: "ops/b", description: "Bravo.", type: "agent", layer: "ops" };
const c = { artifact_id: "ops/c", description: "Charlie.", type: "skill", layer: "team" };

function diag(
  subject: typeof a,
  code: string,
  peers?: { peer: typeof a; similarity: number; computed_at?: string }[],
): OverlapDiagnostic {
  return {
    ...subject,
    code,
    severity: "warning",
    message: `${code} on ${subject.artifact_id}`,
    peers: peers?.map((p) => ({ ...p.peer, similarity: p.similarity, computed_at: p.computed_at })),
  };
}

function reportOf(diagnostics: OverlapDiagnostic[], state = "on"): OverlapReport {
  return {
    semantic_overlap: { state, threshold: state === "on" ? 0.8 : null, model: null, computed: null, total: null },
    diagnostics,
    counts: { error: 0, warning: diagnostics.length, info: 0 },
  };
}

// Spec: §13.10
describe("the overlap report's grouping", () => {
  const report = reportOf([
    diag(a, "lint.colliding_descriptions", [{ peer: c, similarity: 0.7 }, { peer: b, similarity: 0.65 }]),
    diag(a, "lint.semantic_overlap", [{ peer: b, similarity: 0.93, computed_at: "2026-11-16T03:00:12Z" }]),
    diag(a, "lint.thin_description"),
    diag(b, "lint.semantic_overlap", [{ peer: a, similarity: 0.93 }]),
    diag(c, "lint.thin_description"),
  ]);

  it("makes one entry per artifact with overlap and none for a thin description alone", () => {
    expect(clusterEntries(report).map((entry) => entry.artifact.artifact_id)).toEqual(["ops/a", "ops/b"]);
  });

  it("states the semantic message first and merges a peer both checks reached into one row", () => {
    const entry = overlapEntry(report, "ops/a");
    expect(entry?.diagnostics.map((d) => d.code)).toEqual(["lint.semantic_overlap", "lint.colliding_descriptions"]);
    expect(entry?.peers).toEqual([
      {
        peer: b,
        similarity: 0.93,
        wordOverlap: 0.65,
        computedAt: "2026-11-16T03:00:12Z",
      },
      { peer: c, wordOverlap: 0.7 },
    ]);
    expect(entry?.strongestSimilarity).toBe(0.93);
    expect(entry?.strongestWordOverlap).toBe(0.7);
  });

  it("reads an artifact the report holds nothing for as no overlap", () => {
    expect(overlapEntry(report, "ops/hidden")).toBeNull();
    expect(overlapEntry(report, "ops/c")).toBeNull();
    expect(thinDiagnostic(report, "ops/c")?.code).toBe("lint.thin_description");
    expect(thinDiagnostic(report, "ops/b")).toBeNull();
  });

  it("states each entry's strongest scores in their own formats", () => {
    const [first, second] = clusterEntries(report);
    expect(strongestFigure(first, true)).toBe("0.93 · 70%");
    expect(strongestFigure(first, false)).toBe("70%");
    expect(strongestFigure(second, true)).toBe("0.93");
  });
});

describe("the score formats", () => {
  // A similarity just short of a threshold never reads as the threshold.
  it("cuts a similarity down rather than rounding it up", () => {
    expect(formatSimilarity(0.9129)).toBe("0.912");
    expect(formatStrongest(0.8496)).toBe("0.84");
    expect(formatStrongest(0.8499999)).toBe("0.84");
    expect(formatStrongest(0.85)).toBe("0.85");
  });

  it("states a word overlap as a whole percentage inside 0 to 100", () => {
    expect(formatWordOverlap(0.729)).toBe("72%");
    expect(formatWordOverlap(1.2)).toBe("100%");
    expect(formatWordOverlap(-0.1)).toBe("0%");
  });

  it("states a threshold at two decimals unless the registry reported more", () => {
    expect(formatThreshold(0.85)).toBe("0.85");
    expect(formatThreshold(0.825)).toBe("0.825");
    expect(clampThreshold(0.2)).toBe(0.5);
    expect(clampThreshold(1.4)).toBe(1);
  });

  it("spells a count out below ten", () => {
    expect(countWord(3)).toBe("Three");
    expect(countWord(12)).toBe("12");
  });
});

// Spec: §7.3.1, §13.10
describe("the overlap client", () => {
  it("sends the threshold at the control's two decimals and the artifact as given", async () => {
    const fetched: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        fetched.push(String(input));
        return Promise.resolve(new Response("{}", { status: 200, headers: { "content-type": "application/json" } }));
      }),
    );
    const answered = await overlapReport({ threshold: 0.1 + 0.75, artifact: "ops/a" });
    expect(fetched).toEqual(["/v1/overlap?threshold=0.85&artifact=ops%2Fa"]);
    // A body missing a member reads as the empty report with the check off.
    expect(answered).toEqual({
      semantic_overlap: { state: "off", threshold: null, model: null, computed: null, total: null },
      diagnostics: [],
      counts: { error: 0, warning: 0, info: 0 },
    });
  });
});

describe("the similarity components", () => {
  it("draws a missing score as a dash that names the absence", () => {
    render(<ScoreBar kind="similarity" />);
    expect(screen.getByText("—").getAttribute("aria-hidden")).toBe("true");
    expect(screen.getByText("none")).toBeTruthy();
  });

  it("draws each score with its own mark", () => {
    const { container } = render(
      <>
        <ScoreBar kind="similarity" value={0.912} />
        <ScoreBar kind="word" value={0.72} />
      </>,
    );
    expect(container.querySelector(".score-similarity")?.textContent).toBe("0.912");
    expect(container.querySelector(".score-word")?.textContent).toBe("72%");
    expect((container.querySelector(".score-word .score-fill") as HTMLElement).style.width).toBe("72%");
  });

  it("reports the value the reader drags the slider to and states it as a lower bound", () => {
    const onChange = vi.fn();
    render(<RangeSlider label="SIMILARITY" min={0.5} max={1} step={0.01} value={0.85} onChange={onChange} />);
    const input = screen.getByLabelText("SIMILARITY");
    expect(input.getAttribute("aria-valuetext")).toBe("at least 0.85");
    expect(screen.getByText("≥ 0.85")).toBeTruthy();
    expect(screen.getByText("0.5")).toBeTruthy();
    expect(screen.getByText("1.0")).toBeTruthy();
    fireEvent.change(input, { target: { value: "0.9" } });
    expect(onChange).toHaveBeenCalledWith(0.9);
  });

  it("drops the two ends of the range in the compact placement", () => {
    render(
      <RangeSlider label="SIMILARITY" min={0.5} max={1} step={0.01} value={0.97} onChange={() => {}} showEnds={false} />,
    );
    expect(screen.queryByText("0.5")).toBeNull();
    expect(screen.getByText("≥ 0.97")).toBeTruthy();
  });

  it("states no box for an artifact with no overlap message", () => {
    const { container } = render(<MessageBox diagnostics={[]} />);
    expect(container.innerHTML).toBe("");
  });

  it("marks a semantic message with the accent dot and a word message with the grey one", () => {
    const { container } = render(
      <MessageBox
        diagnostics={[diag(a, "lint.semantic_overlap", []), diag(a, "lint.colliding_descriptions", [])]}
      />,
    );
    const dots = [...container.querySelectorAll(".message-dot")].map((dot) => dot.className);
    expect(dots).toEqual(["message-dot message-dot-similarity", "message-dot message-dot-word"]);
  });

  it("links each peer on the page's table and draws no selection there", () => {
    const entry = overlapEntry(
      reportOf([diag(a, "lint.semantic_overlap", [{ peer: b, similarity: 0.9 }])]),
      "ops/a",
    );
    render(<OverlapTable rows={entry?.peers ?? []} showSimilarity peerHref={(id) => `#/artifact/${id}`} />);
    expect(screen.getByRole("link", { name: "ops/b" }).getAttribute("href")).toBe("#/artifact/ops/b");
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("compares two artifacts without a computed time where the semantic score is absent", () => {
    render(
      <DescriptionCompare
        subject={{ ...a, description: "" }}
        row={{ peer: b, wordOverlap: 0.7 }}
        showSimilarity
        href="#/artifact/ops%2Fb"
        now={Date.now()}
      />,
    );
    expect(screen.getByText("word overlap 70%")).toBeTruthy();
    expect(screen.queryByText(/computed/)).toBeNull();
    expect(screen.getByText("No description.")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Open b →" }).getAttribute("href")).toBe("#/artifact/ops%2Fb");
  });

  it("leaves the similarity out of a list row when the check is off", () => {
    const entries = clusterEntries(
      reportOf([diag(a, "lint.colliding_descriptions", [{ peer: b, similarity: 0.66 }])], "off"),
    );
    const onSelect = vi.fn();
    render(<ClusterList entries={entries} selected="ops/a" onSelect={onSelect} showSimilarity={false} />);
    expect(screen.getByText("1 artifact")).toBeTruthy();
    const row = screen.getByRole("button", { name: /ops\/a/ });
    expect(row.textContent).toContain("66%");
    fireEvent.click(row);
    expect(onSelect).toHaveBeenCalledWith("ops/a");
  });
});

// jsdom performs no layout, so the cases read the declarations that reach
// each element. Spec: §13.10
describe("the similarity layout", () => {
  function styled(className: string, tag = "div"): CSSStyleDeclaration {
    const element = document.createElement(tag);
    element.className = className;
    document.body.appendChild(element);
    return window.getComputedStyle(element);
  }

  it("scrolls the cluster list and the detail apart under a header that stays put", () => {
    expect(styled("cluster-list").overflowY).toBe("auto");
    expect(styled("cluster-detail").overflowY).toBe("auto");
    expect(styled("cluster-list-head").position).toBe("sticky");
    expect(styled("similarity-panel").height).toBe("500px");
  });

  it("drops the similarity column from the table's grid when the check is off", () => {
    expect(styled("overlap-grid overlap-grid-both").gridTemplateColumns).toBe("minmax(0, 1fr) 120px 120px 100px");
    expect(styled("overlap-grid overlap-grid-word").gridTemplateColumns).toBe("minmax(0, 1fr) 120px 100px");
  });

  it("cuts a long message to one line", () => {
    const text = styled("message-text", "span");
    expect(text.whiteSpace).toBe("nowrap");
    expect(text.textOverflow).toBe("ellipsis");
  });
});
