#!/usr/bin/env python3
#
# close-build-gaps.py
#
# Standalone driver that closes every OPEN finding in BUILD-GAPS.md and
# builds the documentation end-to-end tests catalogued in TEST-GAPS.md,
# by invoking `claude -p` (non-interactive) once per "agent". It is the
# script form of the podium-spec-gap-remediation workflow and keeps the
# same phases:
#
#   Plan      one claude session reads the OPEN findings + pending doc
#             sources and writes a batch plan (JSON) to tmp/.
#   DocTests  one claude session per pending documentation source builds
#             its test/e2e/docs_<slug>_test.go file.
#   Fixes     one claude session per batch of <=8 related findings:
#             implement to spec, add tests, build/test green, commit,
#             mark the findings CLOSED in BUILD-GAPS.md.
#   Verify    one claude session runs the build + test suite and reports.
#
# Each `claude -p` is stateless; durable state lives in git history and in
# BUILD-GAPS.md (OPEN -> CLOSED / DEFERRED), so the script is safely
# re-runnable and resumes where it left off. Batches run STRICTLY
# SEQUENTIALLY because they share one working tree and one git index.
#
# Robustness (learned from the workflow run that stalled on a blocking
# command): every invocation runs in its own process group under a hard
# timeout; on timeout the whole group is killed (so a hung `go test` or a
# server subprocess dies with it) and the batch is retried by resuming the
# session. A single bad batch is isolated and never aborts the campaign.
#
# Logs land in <repo>/tmp/. close-build-gaps.log collects each session's
# full summary; summary.log keeps one progress line per phase. The running
# log path is exported as REMEDIATION_RUNNING_LOG so each fresh session can
# read the tail for continuity.
#
# Usage (run from the Podium repo root):
#   ./close-build-gaps.py
#   ./close-build-gaps.py --max-iter 50 --fix-cap 4
#   ./close-build-gaps.py --skip-doctests
#   ./close-build-gaps.py --timeout 3600
#   ./close-build-gaps.py --dry-run        # print the prompts; do not invoke
#

import argparse
import json
import os
import re
import signal
import subprocess
import sys
import time
from datetime import datetime, timezone

# --------------------------------------------------------------------------
# Configuration / defaults
# --------------------------------------------------------------------------

REPO = os.getcwd()
LOG_DIR = os.path.join(REPO, "tmp")
BUILD_GAPS = os.path.join(REPO, "BUILD-GAPS.md")
TEST_GAPS = os.path.join(REPO, "TEST-GAPS.md")
PLAN_PATH = os.path.join(LOG_DIR, "build-gaps-plan.json")
RUNNING_LOG = os.path.join(LOG_DIR, "close-build-gaps.log")
SUMMARY_LOG = os.path.join(LOG_DIR, "summary.log")

DEFAULTS = {
    "max_iter": 1000,
    "fix_cap": 6,          # fix batches processed per outer iteration before re-planning + verify
    "timeout": 14400,      # seconds per batch claude invocation (4 h)
    "plan_timeout": 3600,  # seconds for the plan session (60 min)
    "verify_timeout": 1800,  # seconds for the verify session (30 min)
    "retries": 5,          # resume-retries after a failed/timed-out batch
    "model": "claude-opus-4-8",
    "effort": "xhigh",
    "no_progress_limit": 5,  # abort if this many iterations close nothing and build nothing
}

# Live backend services (Postgres + MinIO object store) for the Tier 2 tests,
# injected into every `claude -p` subprocess so findings that need a real
# backend (Postgres schema-per-org / row-level security; object-store delivery)
# are implemented and VERIFIED against it rather than deferred. Values match the
# Makefile `test-live` target and the dev-deps scripts. Each is applied with
# setdefault, so a value the operator already exported wins. The live tests
# self-skip when the service is unreachable, so injecting these is safe even if
# the services happen to be down.
LIVE_ENV = {
    "PODIUM_POSTGRES_DSN": "postgres://podium:podium@localhost:5432/podium?sslmode=disable",
    # The endpoint is a URL whose scheme selects TLS (ParseS3Endpoint, §13.12);
    # http:// is required for a plaintext local MinIO. A bare host defaults to
    # HTTPS and fails against MinIO with an HTTP/HTTPS mismatch.
    "PODIUM_S3_ENDPOINT": "http://localhost:9000",
    "PODIUM_S3_BUCKET": "podium",
    "PODIUM_S3_ACCESS_KEY_ID": "minioadmin",
    "PODIUM_S3_SECRET_ACCESS_KEY": "minioadmin",
    "PODIUM_S3_USE_SSL": "false",
}

# --------------------------------------------------------------------------
# Prompts
# --------------------------------------------------------------------------

GROUND_RULES = """GROUND RULES (apply to every action this session):

A. SPEC IS THE SOURCE OF TRUTH AND READ-ONLY. Never create, edit, move, or
   delete anything under spec/. Cite the relevant spec section in code
   comments and test names (e.g. // spec: SS X.Y). If a fix would require
   changing the spec, do not change it: implement to match the spec, or if
   the spec is genuinely ambiguous or self-contradictory, defer the finding
   with a note explaining why.

B. RE-READ AND RE-VERIFY BEFORE FIXING. The file:line pointers in a finding
   may be stale; search the codebase broadly and read enough surrounding
   context before changing anything. If a finding is already resolved by an
   earlier batch, mark it CLOSED with a one-line note citing the resolving
   commit and move on.

C. WORK ONLY ON THE CURRENT GIT BRANCH. Do not create or switch branches.

D. KEEP THE TREE GREEN AND COMMITTED. gofmt the files you change, run
   `go build ./...` (it must pass), and run `go vet` on the packages you
   touched. Make one commit per batch whose message names the finding IDs
   (or the documentation sources). After committing, `git status` must be
   clean. Never commit a tree where `go build` fails.

E. ALWAYS WRITE TESTS for code you create or modify: unit tests beside the
   code first, then integration tests under test/integration and
   end-to-end tests under test/e2e where technically possible. Tests must
   exercise the spec's corner cases (empty, error, concurrent, boundary,
   and any spec-named failure modes), must fail without your change, and
   must pass with it. Reference the spec section in the test name or a
   // spec: comment. Reuse the harness patterns in test/integration and
   test/conformance.

F. ANTI-HANG -- THIS IS CRITICAL. Never run a blocking, interactive, or
   unbounded command in the foreground:
     - Run `go test` with an explicit timeout, scoped to the packages you
       touched, e.g. `go test -timeout 120s ./pkg/<pkg>/...`. Do NOT run a
       bare `go test ./...` (it is slow and can hang on e2e tests).
     - Never run `podium serve`, `podium-mcp`, any `... --watch`, or
       `podium login` in the foreground. If a test must drive a server or the
       MCP subprocess, the TEST owns its lifecycle: start it as a child
       process, use context deadlines and bounded reads, and always tear it
       down (t.Cleanup or defer). Every end-to-end test must have a hard
       timeout and must never block forever.
     - TUI EXCEPTION: developing the Podium TUI is allowed (for example the
       `podium sync override` and `podium profile edit` interactive modes,
       F-7.5.1). You may build and run that TUI. Do NOT block on it
       interactively in the foreground: drive it from tests via scripted
       stdin or a pseudo-terminal under a hard timeout with teardown, and keep
       a non-TTY fallback path. The TUI under test must never block forever.
     - Redirect stdin from /dev/null for any non-TUI command that might read it.

G. REUSE existing packages and patterns before creating new ones. Prefer
   small functions and modules; add comments only to explain why.

H. NO BACKWARD-COMPATIBILITY SHIMS. The codebase is pre-deployment; change
   interfaces freely.

I. CONTINUITY. A running log of prior sessions is at the path in the
   REMEDIATION_RUNNING_LOG environment variable. When it exists and is
   non-empty, read its tail (e.g. `tail -200 "$REMEDIATION_RUNNING_LOG"`)
   before you start so you do not re-attempt finished work.

J. LIVE BACKENDS ARE AVAILABLE -- USE THEM. A real Postgres and a MinIO
   object store run locally, Docker is available, and their connection env
   vars are already set in your environment: PODIUM_POSTGRES_DSN for Postgres
   and the PODIUM_S3_* vars for the object store. Findings that require a real
   backend -- Postgres tenancy/isolation (schema-per-org + row-level security)
   and object-store delivery (presigned manifest bodies / bundled resources)
   -- MUST be implemented and VERIFIED against these live services. Do not
   defer such a finding for "no backend available." Run the live tests for the
   packages you touch, e.g.
     PODIUM_POSTGRES_DSN="$PODIUM_POSTGRES_DSN" go test -timeout 300s ./pkg/store/...
   and the object-store / resource-delivery integration + e2e tests. These
   self-skip ONLY when the service is genuinely unreachable, so a skip means
   the service is down: report that rather than marking the finding done.
"""

RESUME_PROMPT = (
    "Resume the prior batch. Re-read BUILD-GAPS.md and inspect the working "
    "tree (git status, git diff) to see the current state, then finish any "
    "in-flight work cleanly: gofmt, `go build ./...`, commit, and mark the "
    "findings CLOSED (or DEFERRED) in BUILD-GAPS.md. Do NOT run any "
    "long-running, interactive, or blocking command, and do NOT run a bare "
    "`go test ./...`. Then exit. Keep your summary under 150 words."
)

VERIFY_PROMPT = (
    "VERIFY phase. Do not modify any files.\n"
    "Run, from the repo root:\n"
    "  go build ./...\n"
    "  go vet ./...\n"
    "  go test -timeout 300s ./... -count=1\n"
    "(The live backend env is set for you -- PODIUM_POSTGRES_DSN and the "
    "PODIUM_S3_* vars -- so the Tier 2 Postgres and object-store tests run "
    "against the local Postgres + MinIO. They self-skip only when a service is "
    "unreachable; note any such skip. The -timeout bounds each package so "
    "nothing hangs.)\n"
    "Report, concisely: whether `go build ./...` passed, whether `go vet` "
    "passed, whether the test suite passed, and a one-line entry per failing "
    "package with the cause. Do not attempt fixes; this is a read-only "
    "health check."
)


def plan_prompt() -> str:
    # Built by concatenation so the literal JSON braces below are not
    # mistaken for format placeholders.
    return (
        "You are the PLAN phase of the Podium spec-gap remediation, run from "
        + REPO + ". Do NOT modify any file except the plan JSON named below.\n\n"
        "Read BUILD-GAPS.md and TEST-GAPS.md.\n\n"
        "1. Using grep, collect every OPEN finding id from the DETAILED "
        "headings in BUILD-GAPS.md -- lines that start with '### - [ ] F-' "
        "and end with 'OPEN'. Report the total as openFindingCount.\n\n"
        "2. Group the OPEN findings into fix batches of AT MOST 8 closely "
        "related findings. Cluster by spec subsection / subsystem (for "
        "example all open F-6.6.x together) and by any text-flagged "
        "duplicates, so one fix can close several; split a subsection with "
        "more than 8 open findings into fix-6.6-a, fix-6.6-b, and so on. "
        "Order batches so foundational concerns (manifest and schema "
        "parsing, core types) come before dependents, and so two batches "
        "that edit the same files are not adjacent. For each batch give a "
        "stable id (such as fix-6.6 or fix-4.5-a), its finding ids, the "
        "subsystem, and the files it will likely touch.\n\n"
        "3. For documentation end-to-end tests: enumerate the D-<slug> "
        "sources in TEST-GAPS.md (sections headed '### D-<slug> -- <path>'). "
        "A source is already built if " + REPO + "/test/e2e/docs_<slug>_test.go "
        "exists -- check with ls. Group the sources that are NOT yet built "
        "into a few doc batches of related sources (for example "
        "doc-getting-started, doc-authoring, doc-consuming, doc-deployment), "
        "each listing its docSources.\n\n"
        "Write the plan as a single JSON object to this exact path:\n  "
        + PLAN_PATH + "\n\n"
        "JSON shape (no extra prose in the file):\n"
        '{\n'
        '  "openFindingCount": <int>,\n'
        '  "fixBatches": [ {"id": "fix-6.6", "subsystem": "mcp materialization", "findingIds": ["F-6.6.1","F-6.6.2"], "files": ["cmd/podium-mcp/main.go"]} ],\n'
        '  "docBatches": [ {"id": "doc-getting-started", "docSources": ["D-readme","D-quickstart"]} ]\n'
        '}\n\n'
        "Then print a one-line confirmation of the counts. Do not modify any "
        "other file."
    )


def fix_prompt(batch: dict) -> str:
    bid = batch.get("id", "fix-batch")
    subsystem = batch.get("subsystem", "")
    ids = ", ".join(batch.get("findingIds", []))
    return (
        "You are fixing one cohesive batch of Podium spec-conformance "
        "findings, run from " + REPO + ".\n\n"
        "BATCH " + bid + " (subsystem: " + subsystem + "). "
        "Findings to fix: " + ids + ".\n\n"
        + GROUND_RULES + "\n"
        "TASK THIS SESSION:\n"
        "1. For each finding id, read its full detailed block in "
        "BUILD-GAPS.md (the heading plus the (gap)/(bug)/(inconsistency) "
        "description, file:line evidence, and 'Suggested direction').\n"
        "2. Re-read the cited spec section(s) under spec/ (read-only) to "
        "confirm the exact required behavior. The spec wins over a finding's "
        "suggested direction if they differ.\n"
        "3. Read the implementation broadly and implement the fix so the "
        "code conforms to the spec.\n"
        "4. Add test coverage per ground rule E (unit + integration + e2e "
        "where technically possible), exercising the spec's corner cases.\n"
        "5. If any test under test/e2e is currently skipped with a message "
        "that references one of this batch's finding ids, remove the skip "
        "and make the test pass.\n"
        "6. gofmt; `go build ./...`; scoped `go test -timeout 120s` on the "
        "packages you touched; `go vet` on them. Iterate until green. Obey "
        "the ANTI-HANG rule.\n"
        "7. In BUILD-GAPS.md, mark each finding you actually fixed CLOSED: "
        "change its heading from '- [ ] <id> ... OPEN' to '- [x] <id> ... "
        "CLOSED', and append a one or two sentence Resolution note citing "
        "the commit SHA. If you cannot complete a finding, revert that "
        "finding's changes (so the tree stays green) and mark it "
        "'- [ ] <id> ... DEFERRED' with a reason. Do not touch the summary "
        "tables or the high-severity list.\n"
        "8. `git add -A` and commit with a message naming the fixed finding "
        "ids. Confirm `git status` is clean.\n"
        "9. Before exiting, re-attempt any DEFERRED finding that this batch "
        "directly unblocks (direct unblocks only).\n\n"
        "Output a TIGHT summary (<=200 words): findings closed (id + "
        "half-line), findings deferred (id + reason, or none), commit SHAs, "
        "and tests added (count + tier). Do not restate the rules or diffs."
    )


def doc_prompt(batch: dict) -> str:
    bid = batch.get("id", "doc-batch")
    sources = ", ".join(batch.get("docSources", []))
    return (
        "You are building end-to-end tests for Podium documentation, run "
        "from " + REPO + ".\n\n"
        "DOC TEST BATCH " + bid + ". Documentation sources to cover: "
        + sources + ".\n\n"
        + GROUND_RULES + "\n"
        "TASK THIS SESSION:\n"
        "1. For each source, read its section in TEST-GAPS.md (the block "
        "'### D-<slug> -- <doc path>' with its T-D-... test specifications) "
        "and read the underlying documentation file it points to.\n"
        "2. Implement the specified tests as Go end-to-end tests under "
        "test/e2e/, one file per source named test/e2e/docs_<slug>_test.go "
        "(slug is the D-<slug> identifier). Drive the real surface -- build "
        "and exec the CLI (cmd/podium), spawn the MCP server over stdio "
        "(cmd/podium-mcp), or start the HTTP server (cmd/podium-server) -- "
        "and assert the observable outcomes the documentation and spec "
        "claim. Obey the ANTI-HANG rule: the test owns every process "
        "lifecycle with timeouts and teardown.\n"
        "3. Where a documented behavior is currently broken or unimplemented "
        "per a known BUILD-GAPS finding, still write the test but call "
        "t.Skip with a message of the form 'blocked by F-x.y.z: <reason>' so "
        "the suite stays green and the test encodes the acceptance "
        "criterion. Search BUILD-GAPS.md to find the blocking finding id.\n"
        "4. gofmt; `go build ./...`; `go test -timeout 300s ./test/e2e/...` "
        "must pass (skipped tests are fine). Do not break any other "
        "package.\n"
        "5. `git add -A` and commit with a message naming the documentation "
        "sources covered. Confirm `git status` is clean.\n\n"
        "Output a TIGHT summary (<=150 words): tests added (paths + count), "
        "tests skipped (with the blocking finding ids), and the commit SHA."
    )


# --------------------------------------------------------------------------
# Helpers
# --------------------------------------------------------------------------

def now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def log_summary(line: str) -> None:
    stamped = "[" + now() + "] " + line
    print(stamped, flush=True)
    with open(SUMMARY_LOG, "a", encoding="utf-8") as f:
        f.write(stamped + "\n")


def log_running(text: str) -> None:
    with open(RUNNING_LOG, "a", encoding="utf-8") as f:
        f.write(text + "\n")


def count_headings(status_word: str) -> int:
    """Count detailed finding headings ending in the given status word."""
    n = 0
    try:
        with open(BUILD_GAPS, encoding="utf-8") as f:
            for line in f:
                s = line.rstrip()
                if s.startswith("### - [") and " F-" in s and s.endswith(status_word):
                    n += 1
    except FileNotFoundError:
        pass
    return n


def count_open() -> int:
    return count_headings("OPEN")


def count_closed() -> int:
    return count_headings("CLOSED")


def count_deferred() -> int:
    return count_headings("DEFERRED")


def load_plan(path: str):
    """Read the plan JSON the Plan session wrote; tolerate stray prose."""
    try:
        with open(path, encoding="utf-8") as f:
            txt = f.read()
    except FileNotFoundError:
        return None
    try:
        return json.loads(txt)
    except Exception:
        i, j = txt.find("{"), txt.rfind("}")
        if 0 <= i < j:
            try:
                return json.loads(txt[i:j + 1])
            except Exception:
                return None
    return None


def parse_envelope(stdout_text: str):
    """Extract (session_id, result_text) from a `claude -p --output-format
    json` stdout payload. Falls back to raw text when it is not JSON."""
    session, result = "", ""
    try:
        d = json.loads(stdout_text)
        session = d.get("session_id", "") or ""
        result = d.get("result") or d.get("error") or ""
    except Exception:
        result = stdout_text.strip()
    return session, result


# --------------------------------------------------------------------------
# claude -p invocation (process-group isolated, hard timeout, group-kill)
# --------------------------------------------------------------------------

class ClaudeResult:
    def __init__(self, rc, session, result, timed_out):
        self.rc = rc
        self.session = session
        self.result = result
        self.timed_out = timed_out


def run_claude(prompt: str, resume_id, timeout: int, cfg) -> ClaudeResult:
    cmd = [
        "claude", "-p",
        "--permission-mode", "auto",
        "--add-dir", REPO,
        "--model", cfg["model"],
        "--output-format", "json",
    ]
    if cfg["effort"]:
        cmd += ["--effort", cfg["effort"]]
    if resume_id:
        cmd += ["--resume", resume_id]
    cmd.append(prompt)

    env = dict(os.environ)
    env["REMEDIATION_RUNNING_LOG"] = RUNNING_LOG
    # Expose the live backends (Postgres + MinIO) to every batch so
    # backend-dependent findings are verified, not deferred. setdefault keeps
    # any value the operator already exported.
    for _k, _v in LIVE_ENV.items():
        env.setdefault(_k, _v)

    # start_new_session=True puts claude (and any go test / server children)
    # in its own process group so a timeout can kill the whole tree.
    try:
        proc = subprocess.Popen(
            cmd, cwd=REPO, env=env,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True, start_new_session=True,
        )
    except FileNotFoundError:
        log_running("ERROR: `claude` not found on PATH.")
        return ClaudeResult(127, "", "claude not found on PATH", False)

    timed_out = False
    try:
        out, err = proc.communicate(timeout=timeout)
        rc = proc.returncode
    except subprocess.TimeoutExpired:
        timed_out = True
        try:
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
        except Exception:
            pass
        try:
            out, err = proc.communicate(timeout=30)
        except Exception:
            out, err = "", ""
        rc = 124

    session, result = parse_envelope(out or "")
    if rc != 0 and not result:
        tail = (err or "").strip()[-1500:]
        result = ("(timed out after %ds)" % timeout) if timed_out else ("(rc=%d) %s" % (rc, tail))
    return ClaudeResult(rc, session, result, timed_out)


def run_batch(label: str, prompt: str, timeout: int, cfg, retries=None) -> ClaudeResult:
    """Run one phase/batch as a fresh claude session, retrying on failure by
    resuming the captured session. Never raises; a persistently failing
    batch is logged and left for a future iteration to retry."""
    if retries is None:
        retries = cfg["retries"]
    log_running("\n========== %s  %s ==========" % (label, now()))
    log_summary("start  %-22s" % label)

    r = run_claude(prompt, None, timeout, cfg)
    log_running(r.result or "(no result)")

    attempt = 0
    while r.rc != 0 and attempt < retries:
        attempt += 1
        retry_timeout = max(600, timeout // 2)
        if r.session:
            log_running("[retry %d/%d resuming session=%s]" % (attempt, retries, r.session))
            r = run_claude(RESUME_PROMPT, r.session, retry_timeout, cfg)
        else:
            log_running("[retry %d/%d no session captured -- fresh start]" % (attempt, retries))
            r = run_claude(prompt, None, retry_timeout, cfg)
        log_running(r.result or "(no result)")

    status = "ok" if r.rc == 0 else ("timeout" if r.timed_out else ("rc=%d" % r.rc))
    log_summary("done   %-22s %s" % (label, status))
    return r


# --------------------------------------------------------------------------
# Main
# --------------------------------------------------------------------------

def parse_args():
    p = argparse.ArgumentParser(
        description="Close BUILD-GAPS findings and build TEST-GAPS doc tests via claude -p.",
        add_help=True,
    )
    p.add_argument("--max-iter", type=int, default=DEFAULTS["max_iter"])
    p.add_argument("--fix-cap", type=int, default=DEFAULTS["fix_cap"],
                   help="fix batches per outer iteration before re-plan + verify")
    p.add_argument("--timeout", type=int, default=DEFAULTS["timeout"],
                   help="seconds per fix/doc batch claude invocation")
    p.add_argument("--plan-timeout", type=int, default=DEFAULTS["plan_timeout"])
    p.add_argument("--verify-timeout", type=int, default=DEFAULTS["verify_timeout"])
    p.add_argument("--retries", type=int, default=DEFAULTS["retries"])
    p.add_argument("--model", default=DEFAULTS["model"])
    p.add_argument("--effort", default=DEFAULTS["effort"],
                   help='claude --effort level; pass "" to omit')
    p.add_argument("--skip-doctests", action="store_true",
                   help="skip the DocTests phase (fixes only)")
    p.add_argument("--no-verify", action="store_true",
                   help="skip the Verify phase at the end of each iteration")
    p.add_argument("--allow-main", action="store_true",
                   help="permit running on the main/master branch")
    p.add_argument("--dry-run", action="store_true",
                   help="print the prompts that would be sent; do not invoke claude")
    return p.parse_args()


def current_branch() -> str:
    try:
        return subprocess.run(
            ["git", "rev-parse", "--abbrev-ref", "HEAD"],
            cwd=REPO, capture_output=True, text=True, timeout=15,
        ).stdout.strip()
    except Exception:
        return ""


def preflight(args) -> None:
    if not os.path.isfile(BUILD_GAPS):
        sys.exit("error: %s not found -- run from the Podium repo root." % BUILD_GAPS)
    if not os.path.isfile(TEST_GAPS) and not args.skip_doctests:
        sys.exit("error: %s not found (needed for the DocTests phase; use --skip-doctests to omit)." % TEST_GAPS)
    if not args.dry_run:
        from shutil import which
        if which("claude") is None:
            sys.exit("error: `claude` not found on PATH.")
        if which("go") is None:
            print("warning: `go` not found on PATH; batches will be unable to build or test.", file=sys.stderr)
    br = current_branch()
    if br in ("main", "master") and not args.allow_main and not args.dry_run:
        sys.exit("error: on branch %r; refusing to commit fixes here. "
                 "Check out a working branch, or pass --allow-main." % br)


def main() -> int:
    args = parse_args()
    cfg = {
        "model": args.model,
        "effort": args.effort,
        "retries": args.retries,
    }
    preflight(args)
    os.makedirs(LOG_DIR, exist_ok=True)

    if args.dry_run:
        print("=== DRY RUN -- prompts that would be sent ===\n")
        print("----- PLAN -----\n" + plan_prompt() + "\n")
        print("----- FIX (example batch) -----\n"
              + fix_prompt({"id": "fix-6.6", "subsystem": "mcp materialization",
                            "findingIds": ["F-6.6.1", "F-6.6.2"]}) + "\n")
        print("----- DOC (example batch) -----\n"
              + doc_prompt({"id": "doc-getting-started",
                            "docSources": ["D-readme", "D-quickstart"]}) + "\n")
        print("----- VERIFY -----\n" + VERIFY_PROMPT + "\n")
        return 0

    branch = current_branch()
    log_summary("CAMPAIGN START branch=%s open=%d closed=%d deferred=%d model=%s"
                % (branch, count_open(), count_closed(), count_deferred(), cfg["model"]))

    no_progress = 0
    it = 0
    while it < args.max_iter:
        it += 1
        before_open = count_open()
        before_closed = count_closed()

        # ---- Plan -------------------------------------------------------
        try:
            if os.path.exists(PLAN_PATH):
                os.remove(PLAN_PATH)
        except OSError:
            pass
        run_batch("plan[it=%d]" % it, plan_prompt(), args.plan_timeout, cfg, retries=1)
        plan = load_plan(PLAN_PATH)
        if plan is None:
            log_summary("iter=%d plan produced no readable JSON; will retry next iteration" % it)
            no_progress += 1
            if no_progress >= DEFAULTS["no_progress_limit"]:
                log_summary("aborting: %d consecutive iterations made no progress" % no_progress)
                break
            continue

        open_count = plan.get("openFindingCount", before_open)
        doc_batches = [] if args.skip_doctests else (plan.get("docBatches") or [])
        fix_batches = plan.get("fixBatches") or []

        if open_count == 0 and not doc_batches:
            log_summary("iter=%d nothing left: open=0 and no pending doc batches" % it)
            break

        log_summary("iter=%d plan: open=%d fixBatches=%d docBatches=%d (running %d doc + up to %d fix)"
                    % (it, open_count, len(fix_batches), len(doc_batches),
                       len(doc_batches), args.fix_cap))

        # ---- DocTests (sequential; all pending this iteration) ----------
        for b in doc_batches:
            run_batch("doctest:" + b.get("id", "?"), doc_prompt(b), args.timeout, cfg)

        # ---- Fixes (sequential; capped per iteration) -------------------
        for b in fix_batches[: args.fix_cap]:
            run_batch("fix:" + b.get("id", "?"), fix_prompt(b), args.timeout, cfg)

        # ---- Verify -----------------------------------------------------
        if not args.no_verify:
            run_batch("verify[it=%d]" % it, VERIFY_PROMPT, args.verify_timeout, cfg, retries=1)

        after_open = count_open()
        after_closed = count_closed()
        delta = after_closed - before_closed
        log_summary("iter=%d delta=+%d open=%d closed=%d deferred=%d"
                    % (it, delta, after_open, after_closed, count_deferred()))

        progressed = (delta > 0) or (after_open < before_open) or bool(doc_batches)
        no_progress = 0 if progressed else (no_progress + 1)
        if no_progress >= DEFAULTS["no_progress_limit"]:
            log_summary("aborting: %d consecutive iterations closed nothing and built no doc tests"
                        % no_progress)
            break

    final_open = count_open()
    final_closed = count_closed()
    final_deferred = count_deferred()
    log_summary("CAMPAIGN END iter=%d open=%d closed=%d deferred=%d"
                % (it, final_open, final_closed, final_deferred))
    return 0 if final_open == 0 else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print("\ninterrupted; state is durable in git + BUILD-GAPS.md -- re-run to resume.", file=sys.stderr)
        sys.exit(130)
