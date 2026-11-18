// Workspace local overlay merge for the SDK (spec §6.4, §6.4.1).
//
// The overlay is the highest-precedence layer in the caller's effective
// view, merged client-side. search_artifacts fans out to both the registry
// and a local BM25 index over overlay manifests and fuses the two via
// reciprocal rank fusion; load_artifact resolves an overlay artifact ahead
// of the registry. Node's fs/path load lazily so the SDK stays importable
// in edge bundles that never touch the overlay.

import { resolveSkillVersion, synthesizedSkillDocument } from "./skill_document.js";

const OVERLAY_SUBDIR = ".podium/overlay";
const WORD_RE = /[a-z0-9]+/g;

function tokenize(text: string): string[] {
  return text.toLowerCase().match(WORD_RE) ?? [];
}

// resolveOverlayPath applies the §6.4 lookup order: an explicit
// Client(overlayPath) wins over PODIUM_OVERLAY_PATH, which wins over the
// <CWD>/.podium/overlay/ fallback. Returns null when the layer is disabled.
export async function resolveOverlayPath(
  explicit: string | undefined,
  env: string | undefined,
  cwd: string,
): Promise<string | null> {
  if (explicit) return explicit;
  if (env) return env;
  const fs = await import("node:fs/promises");
  const path = await import("node:path");
  const candidate = path.join(cwd || ".", OVERLAY_SUBDIR);
  try {
    const st = await fs.stat(candidate);
    if (st.isDirectory()) return candidate;
  } catch {
    // no CWD overlay; layer disabled
  }
  return null;
}

interface FrontmatterFields {
  type: string;
  version: string;
  description: string;
  tags: string[];
}

// parseFrontmatter hand-parses the top-level fields the SDK ranks and reports
// from an ARTIFACT.md or a SKILL.md: type, version, description, and tags. A
// SKILL.md metadata value is read by metadataVersion and metadataTags instead,
// which take a value only in a form whose line reading equals the YAML
// decoding.
function parseFrontmatter(text: string): { fields: FrontmatterFields; body: string } {
  const fields: FrontmatterFields = { type: "", version: "", description: "", tags: [] };
  if (!text.startsWith("---")) return { fields, body: text };
  const lines = text.split(/\r?\n/);
  let end = -1;
  for (let i = 1; i < lines.length; i++) {
    if (lines[i].trim() === "---") {
      end = i;
      break;
    }
  }
  if (end === -1) return { fields, body: text };
  const body = lines.slice(end + 1).join("\n");
  let pendingTags = false;
  const tags: string[] = [];
  const unquote = (s: string): string => s.trim().replace(/^['"]|['"]$/g, "");
  for (const raw of lines.slice(1, end)) {
    if (pendingTags && raw.trimStart().startsWith("- ")) {
      tags.push(unquote(raw.trimStart().slice(2)));
      continue;
    }
    pendingTags = false;
    if (!raw.trim() || raw.trimStart().startsWith("#")) continue;
    if (raw.startsWith(" ")) continue;
    const sep = raw.indexOf(":");
    if (sep === -1) continue;
    const key = raw.slice(0, sep).trim();
    const val = raw.slice(sep + 1).trim();
    if (key === "tags") {
      if (val.startsWith("[") && val.endsWith("]")) {
        for (const t of val.slice(1, -1).split(",")) {
          if (t.trim()) tags.push(unquote(t));
        }
      } else if (val) {
        for (const t of val.split(",")) if (t.trim()) tags.push(unquote(t));
      } else {
        pendingTags = true;
      }
    } else if (key === "type" || key === "version" || key === "description") {
      fields[key] = scalarValue(val);
    }
  }
  fields.tags = tags.filter(Boolean);
  return { fields, body };
}

// YAML_NULLS are the plain scalars yaml.v3 decodes to null, which a string
// field reads as empty, and the empty value, which is null too.
const YAML_NULLS = new Set(["", "~", "null", "Null", "NULL"]);

// scalarValue returns the string a single-line top-level YAML scalar decodes
// to. A quoted value ends at its closing quote, so a trailing comment after it
// is dropped. A plain value ends before a "#" that begins the value or follows
// white space, which starts a comment, and a plain null (~, null, or an empty
// value) reads as empty, as Go's decode reads it into a string field.
// "version: ~" therefore declares no version, and "type: skill # c" declares a
// skill. Escapes inside a quoted value are kept as written. An unclosed quote
// keeps the earlier reading, with the surrounding quotes removed.
//
// Spec: §4.3, §4.3.4
function scalarValue(val: string): string {
  const quote = val[0];
  if (quote === '"' || quote === "'") {
    for (let i = 1; i < val.length; i++) {
      if (quote === '"' && val[i] === "\\") {
        i++;
      } else if (val[i] === quote) {
        if (quote === "'" && val[i + 1] === "'") i++;
        else return val.slice(1, i);
      }
    }
    return val.replace(/^['"]|['"]$/g, "");
  }
  const comment = /(?:^|[ \t])#/.exec(val);
  const plain = (comment ? val.slice(0, comment.index) : val).replace(/[ \t]+$/, "");
  return YAML_NULLS.has(plain) ? "" : plain;
}

// The SKILL.md metadata.tags readable form. A line reader cannot reproduce
// YAML, so metadataTags reads metadata.tags only from one restricted
// single-line form, in a frontmatter laid out so that the line reading equals
// the yaml.v3 decoding Go applies, and returns [] for every other input.
// test/vectors/skill-metadata-tags.json, generated from
// manifest.ResolveStoredSkill, pins the form to Go. The frontmatter is
// delimited as Go's SplitFrontmatter delimits it: the text begins with "---"
// and a line end, lines split at LF with a CR before an LF removed, and the
// frontmatter ends at the first later line that begins with "---". A value is
// in the form when every numbered condition holds:
//   1. Exactly one frontmatter line begins with "metadata:", and nothing but
//      white space follows it.
//   2. The block runs to the next column-0 line that is not a comment. The
//      child indent is the space indentation of its first line that is neither
//      blank nor a comment.
//   3. Exactly one block line at the child indent has the key "tags" (the text
//      before the first ":", trailing spaces and one pair of quotes removed).
//   4. That line is the child indent, "tags:", one or more spaces, the value,
//      and optional trailing spaces, with no tab.
//   5. The value is plain (TAGS_PLAIN and not a null word), or double- or
//      single-quoted printable ASCII with no escape and no inner quote.
//   6. Every later line is blank up to the next block line at or above the
//      child indent, and no later block line holds a "*" that starts an alias
//      or a "!" that starts a tag: yaml.v3 lets an alias key that resolves to
//      "tags", or a tagged key that decodes to it, replace the value with no
//      error.
//   7. Splitting the content on "," and trimming spaces leaves no empty
//      element. The elements, in order, are the tags.
//   8. The frontmatter holds no CR, U+0085, U+2028, U+2029, or U+FEFF, which
//      yaml.v3 reads as, or skips at, a line start the line reader cannot see.
//   9. The first frontmatter line that is neither blank nor a comment begins
//      at column 0, since yaml.v3 decodes only the first document.
//  10. No line before the tags line begins with "..." or "%", holds "[" or
//      "{", or holds a quote at a token start, because each can end the
//      document or open a scalar that swallows later lines. A key followed by
//      a quoted value of the condition 5 form is exempt.
// White space here is the space and the tab only, so no check uses trim(),
// which also strips characters yaml.v3 does not treat as white space.
const TAGS_PLAIN = /^[A-Za-z0-9][A-Za-z0-9 ,._/-]*$/;
const TAGS_DOUBLE = /^"([\x20\x21\x23-\x5b\x5d-\x7e]*)"$/;
const TAGS_SINGLE = /^'([\x20-\x26\x28-\x7e]*)'$/;
const TAGS_NULLS = new Set(["null", "Null", "NULL"]);
// A key and a quoted scalar with no escape and no inner quote, which leaves no
// quoted scalar open; condition 10 exempts such a line before the tags line.
const QUOTED_PAIR_LINE = /^ *[A-Za-z][A-Za-z0-9_-]*: +("[\x20\x21\x23-\x5b\x5d-\x7e]*"|'[\x20-\x26\x28-\x7e]*') *$/;
// yaml.v3 reads the first four as line breaks and skips a U+FEFF at the start
// of a line, so each can start a line the line reader does not see.
const HIDDEN_BREAK = /[\r\u0085\u2028\u2029\ufeff]/;

const isWhite = (ch: string | undefined): boolean => ch === " " || ch === "\t";
const isBlankLine = (line: string): boolean => [...line].every((ch) => isWhite(ch));
const indentOf = (line: string): string => line.slice(0, line.length - line.replace(/^[ \t]+/, "").length);
const isCommentLine = (line: string): boolean => line[indentOf(line).length] === "#";
const trimSpaces = (s: string): string => s.replace(/^ +| +$/g, "");

// frontmatterLines delimits the frontmatter as Go's SplitFrontmatter does: the
// text begins with "---" and an LF or a CR LF, and the frontmatter ends at the
// first later line that begins with "---". Lines split at each LF, with a CR
// directly before the LF removed. It returns null when there is no frontmatter.
function frontmatterLines(text: string): string[] | null {
  if (!text.startsWith("---\n") && !text.startsWith("---\r\n")) return null;
  const parts = text.split("\n");
  const lines = parts.map((l, i) => (i < parts.length - 1 && l.endsWith("\r") ? l.slice(0, -1) : l));
  const end = lines.findIndex((l, i) => i > 0 && l.startsWith("---"));
  return end === -1 ? null : lines.slice(1, end);
}

// lineKey returns the text before the first ":" with trailing spaces and one
// pair of matching surrounding quotes removed, or null with no ":".
function lineKey(rest: string): string | null {
  const sep = rest.indexOf(":");
  if (sep === -1) return null;
  const key = rest.slice(0, sep).replace(/ +$/, "");
  const q = key[0];
  return key.length >= 2 && (q === '"' || q === "'") && key.endsWith(q) ? key.slice(1, -1) : key;
}

// tagsContent returns the content of the tags line's value (conditions 4 and
// 5), or null when the line or the value is outside the form.
function tagsContent(line: string, indent: string): string | null {
  if (line.includes("\t") || !line.startsWith(`${indent}tags:`)) return null;
  const rest = line.slice(indent.length + "tags:".length);
  if (!rest.startsWith(" ")) return null;
  const value = trimSpaces(rest);
  if (TAGS_PLAIN.test(value)) return TAGS_NULLS.has(value) ? null : value;
  const quoted = TAGS_DOUBLE.exec(value) ?? TAGS_SINGLE.exec(value);
  return quoted ? quoted[1] : null;
}

// blankUntilSibling reports whether every line from lines[0] up to the next
// line indented no deeper than indentLen that is neither blank nor a comment
// is blank, so no continuation line extends the tags value (condition 6).
function blankUntilSibling(lines: string[], indentLen: number): boolean {
  for (const line of lines) {
    if (isBlankLine(line)) continue;
    return !isCommentLine(line) && indentOf(line).length <= indentLen;
  }
  return true;
}

// holdsAliasOrTag reports whether a line holds an alias or a tag at a token
// start (condition 6). yaml.v3 compares mapping keys for duplicates only
// between nodes of one kind and tag, so an alias key resolving to tags, or a
// tagged key such as !!binary dGFncw== that decodes to it, replaces the
// earlier value with no error. A token starts at the line start, after white
// space, or after the "[", "{", or "," of a flow collection, which needs no
// space before the next entry. A "*" starts an alias when a non-white
// character follows it, and a "!" always starts a tag. A "*" or a "!" at a
// token start inside a scalar counts too, so the check errs toward reporting
// no value.
function holdsAliasOrTag(line: string): boolean {
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (ch !== "*" && ch !== "!") continue;
    if (i > 0 && !isWhite(line[i - 1]) && !"[{,".includes(line[i - 1])) continue;
    if (ch === "!" || (i + 1 < line.length && !isWhite(line[i + 1]))) return true;
  }
  return false;
}

// opensScalarOrDocumentEnd reports whether a frontmatter line before the tags
// line can end the YAML document or open a quoted scalar or a flow collection
// that continues across later lines and holds the metadata or tags line as
// text (condition 10).
function opensScalarOrDocumentEnd(line: string): boolean {
  if (line.startsWith("...") || line.startsWith("%")) return true;
  if (QUOTED_PAIR_LINE.test(line)) return false;
  if (line.includes("[") || line.includes("{")) return true;
  for (let i = 0; i < line.length; i++) {
    if ((line[i] === '"' || line[i] === "'") && (i === 0 || isWhite(line[i - 1]))) return true;
  }
  return false;
}

// metadataTags returns the tags of a SKILL.md whose metadata.tags value and
// frontmatter are in the readable form, and [] for every input outside it.
// The overlay runs no lint, so a dropped value raises nothing; podium lint
// reports it under the Go rules.
//
// Spec: §4.3.4, §6.4
export function metadataTags(skillMD: string): string[] {
  const fm = frontmatterLines(skillMD);
  if (fm === null || fm.some((l) => HIDDEN_BREAK.test(l))) return [];
  const first = fm.find((l) => !isBlankLine(l) && !isCommentLine(l));
  if (first === undefined || isWhite(first[0])) return [];
  const metaAt = fm.flatMap((l, i) => (l.startsWith("metadata:") ? [i] : []));
  if (metaAt.length !== 1 || !isBlankLine(fm[metaAt[0]].slice("metadata:".length))) return [];
  const start = metaAt[0] + 1;
  let stop = start;
  while (stop < fm.length && !(fm[stop] !== "" && !isWhite(fm[stop][0]) && fm[stop][0] !== "#")) stop++;
  const child = fm.slice(start, stop).find((l) => !isBlankLine(l) && !isCommentLine(l));
  if (child === undefined) return [];
  const indent = indentOf(child);
  if (!/^ +$/.test(indent)) return [];
  const tagsAt: number[] = [];
  for (let i = start; i < stop; i++) {
    if (indentOf(fm[i]) === indent && lineKey(fm[i].slice(indent.length)) === "tags") tagsAt.push(i);
  }
  if (tagsAt.length !== 1) return [];
  const at = tagsAt[0];
  const content = tagsContent(fm[at], indent);
  if (content === null) return [];
  const after = fm.slice(at + 1, stop);
  if (!blankUntilSibling(after, indent.length) || after.some(holdsAliasOrTag)) return [];
  if (fm.slice(0, at).some(opensScalarOrDocumentEnd)) return [];
  const tags = content.split(",").map(trimSpaces);
  return tags.some((t) => t === "") ? [] : tags;
}

// The SKILL.md metadata.version readable form. metadataVersion reads the value
// only where the line reading equals the yaml.v3 decoding Go applies, and
// returns "" for every other input, so the overlay reports the decoded version
// or none (§4.3.4). test/vectors/skill-metadata-version.json, generated from
// manifest.ResolveSkillVersion, pins the form to Go. The frontmatter is
// delimited as for metadataTags, and conditions 8 and 9 of the tags form
// apply. A value is in the form when, in addition:
//   1. Exactly one frontmatter line begins with "metadata:". What follows it
//      is white space, a comment, or a flow mapping that closes on the line.
//   2. In the block form, the block and its child indent are as in condition 2
//      of the tags form, exactly one block line at the child indent has the
//      key "version", and that line is the child indent, "version:", one or
//      more spaces, the value, and optional trailing spaces. Every later line
//      is blank up to the next block line at or above the child indent.
//   3. In the flow form, the mapping holds no comment, exactly one of its
//      top-level entries has the key "version", once unquoted, and the value
//      is that entry's value.
//   4. The value is major.minor.patch, plain or in double or single quotes.
//   5. No line before the version line, or before the metadata line in the
//      flow form, begins with "..." or "%" or leaves a quoted scalar or a flow
//      collection open, because each can end the document or swallow the
//      version line into another value: a multi-line flow mapping such as
//      "openclaw: {" or a quoted scalar continued on the next line.
//   6. No later frontmatter line, and no part of a flow-form mapping, holds a
//      "*" that starts an alias or a "!" that starts a tag, which yaml.v3 lets
//      replace the metadata mapping or its version key with no error.
const VERSION_VALUE = /^(?:([0-9]+\.[0-9]+\.[0-9]+)|"([0-9]+\.[0-9]+\.[0-9]+)"|'([0-9]+\.[0-9]+\.[0-9]+)')$/;

// versionValue returns the version a value in the condition 4 form holds, or
// "".
function versionValue(value: string): string {
  const m = VERSION_VALUE.exec(trimSpaces(value));
  return m ? (m[1] ?? m[2] ?? m[3]) : "";
}

// leavesOpen reports whether a line can end the document or leave a quoted
// scalar or a flow collection open (condition 5). A quote at a token start
// opens a quoted scalar, and a bracket or a brace at a token start opens a
// flow collection; the line leaves it open when it does not close before the
// line ends. A "#" after white space outside a quoted scalar starts a comment,
// which ends the scan. A quote inside a plain scalar, such as say "hi, counts
// as an opener too, so the check errs toward reporting no value.
function leavesOpen(line: string): boolean {
  if (line.startsWith("...") || line.startsWith("%")) return true;
  let depth = 0;
  let quote = "";
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    const prev = i > 0 ? line[i - 1] : " ";
    if (quote) {
      if (quote === '"' && ch === "\\") i++;
      else if (ch === quote && quote === "'" && line[i + 1] === "'") i++;
      else if (ch === quote) quote = "";
    } else if (ch === "#" && isWhite(prev)) {
      break;
    } else if ((ch === '"' || ch === "'") && (isWhite(prev) || (depth > 0 && "[{,:".includes(prev)))) {
      quote = ch;
    } else if ((ch === "[" || ch === "{") && (isWhite(prev) || depth > 0)) {
      depth++;
    } else if ((ch === "]" || ch === "}") && depth > 0) {
      depth--;
    }
  }
  return quote !== "" || depth > 0;
}

// flowMappingEntries splits a single-line YAML flow mapping into its top-level
// entries. Commas inside a nested mapping, a sequence, or a quoted scalar stay
// part of their entry, so a nested version key never reads as the mapping's
// own. It returns null for a value that is not a flow mapping, whose braces do
// not close on the line, that holds a comment, which would continue the
// mapping on a later line, or that carries anything but a comment after the
// closing brace.
function flowMappingEntries(val: string): string[] | null {
  if (!val.startsWith("{")) return null;
  const entries: string[] = [];
  let depth = 0;
  let quote = "";
  let start = 1;
  for (let i = 0; i < val.length; i++) {
    const ch = val[i];
    if (quote) {
      if (quote === '"' && ch === "\\") i++;
      else if (ch === quote) quote = "";
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
    } else if (ch === "#" && isWhite(val[i - 1])) {
      return null;
    } else if (ch === "{" || ch === "[") {
      depth++;
    } else if (ch === "," && depth === 1) {
      entries.push(val.slice(start, i));
      start = i + 1;
    } else if ((ch === "}" || ch === "]") && --depth === 0) {
      entries.push(val.slice(start, i));
      const rest = val.slice(i + 1).replace(/^[ \t]+|[ \t]+$/g, "");
      return rest && !rest.startsWith("#") ? null : entries;
    }
  }
  return null;
}

// flowMetadataVersion returns the version of a flow-form metadata value
// (condition 3), or "".
function flowMetadataVersion(val: string): string {
  const entries = flowMappingEntries(val);
  if (entries === null || holdsAliasOrTag(val)) return "";
  const found: string[] = [];
  for (const entry of entries) {
    const trimmed = entry.replace(/^[ \t]+|[ \t]+$/g, "");
    const sep = trimmed.indexOf(":");
    if (sep === -1) continue;
    const value = trimmed.slice(sep + 1);
    if (value !== "" && !isWhite(value[0])) continue;
    let key = trimmed.slice(0, sep).replace(/[ \t]+$/, "");
    if (key.length >= 2 && (key[0] === '"' || key[0] === "'") && key.endsWith(key[0])) key = key.slice(1, -1);
    if (key === "version") found.push(value);
  }
  return found.length === 1 ? versionValue(found[0]) : "";
}

// blockMetadataVersion returns the version of a block-form metadata value and
// the index of its line (condition 2), or ["", -1]. start is the index of the
// line after metadata:.
function blockMetadataVersion(fm: string[], start: number): [string, number] {
  let stop = start;
  while (stop < fm.length && !(fm[stop] !== "" && !isWhite(fm[stop][0]) && fm[stop][0] !== "#")) stop++;
  const child = fm.slice(start, stop).find((l) => !isBlankLine(l) && !isCommentLine(l));
  if (child === undefined) return ["", -1];
  const indent = indentOf(child);
  if (!/^ +$/.test(indent)) return ["", -1];
  const at: number[] = [];
  for (let i = start; i < stop; i++) {
    if (indentOf(fm[i]) === indent && lineKey(fm[i].slice(indent.length)) === "version") at.push(i);
  }
  if (at.length !== 1 || fm[at[0]].includes("\t") || !fm[at[0]].startsWith(`${indent}version: `)) return ["", -1];
  if (!blankUntilSibling(fm.slice(at[0] + 1, stop), indent.length)) return ["", -1];
  return [versionValue(fm[at[0]].slice(indent.length + "version:".length)), at[0]];
}

// metadataVersion returns the metadata.version of a SKILL.md in the readable
// form, and "" for every input outside it. A version: line nested in another
// key's block, or inside a multi-line flow collection or quoted scalar, is
// never read, so the overlay reports no version the YAML decoding does not
// produce.
//
// Spec: §4.3.4, §6.4
export function metadataVersion(skillMD: string): string {
  const fm = frontmatterLines(skillMD);
  if (fm === null || fm.some((l) => HIDDEN_BREAK.test(l))) return "";
  const first = fm.find((l) => !isBlankLine(l) && !isCommentLine(l));
  if (first === undefined || isWhite(first[0])) return "";
  const metaAt = fm.flatMap((l, i) => (l.startsWith("metadata:") ? [i] : []));
  if (metaAt.length !== 1) return "";
  const rest = fm[metaAt[0]].slice("metadata:".length);
  let version: string;
  let at: number;
  if (isBlankLine(rest) || rest.replace(/^[ \t]+/, "").startsWith("#")) {
    if (rest !== "" && !isWhite(rest[0])) return "";
    [version, at] = blockMetadataVersion(fm, metaAt[0] + 1);
  } else if (rest.startsWith(" ")) {
    [version, at] = [flowMetadataVersion(rest.replace(/^ +/, "")), metaAt[0]];
  } else {
    return "";
  }
  if (version === "" || fm.slice(0, at).some(leavesOpen)) return "";
  if (fm.slice(at + 1).some(holdsAliasOrTag)) return "";
  return version;
}

export interface OverlayArtifact {
  id: string;
  type: string;
  version: string;
  description: string;
  tags: string[];
  frontmatter: string;
  body: string;
  skillRaw: string;
  resources: Record<string, string>;
  tokens: string[];
}

// OverlayDomain holds the workspace-overlay DOMAIN.md fields the §4.5.4
// load_domain merge reads: the frontmatter description, the prose body, the
// include/exclude globs, the unlisted flag, and the discovery knobs (keywords,
// featured, notable_count). It mirrors pkg/manifest.Domain as the merge
// consumes it.
export interface OverlayDomain {
  description: string;
  body: string;
  include: string[];
  exclude: string[];
  unlisted: boolean;
  keywords: string[];
  featured: string[];
  notableCount: number;
}

// parseDomainFrontmatter hand-parses a DOMAIN.md frontmatter block the same way
// parseFrontmatter reads an ARTIFACT.md, with no YAML dependency. It reads the
// top-level description, unlisted (bool), include/exclude lists (inline
// `[a, b]` and block `- x` forms), and the nested discovery: block's keywords,
// featured, and notable_count. The body is everything after the closing `---`.
// Returns null for a malformed file (no frontmatter) so the caller can skip it.
function parseDomainFrontmatter(text: string): OverlayDomain | null {
  if (!text.startsWith("---")) return null;
  const lines = text.split(/\r?\n/);
  let end = -1;
  for (let i = 1; i < lines.length; i++) {
    if (lines[i].trim() === "---") {
      end = i;
      break;
    }
  }
  if (end === -1) return null;
  const body = lines.slice(end + 1).join("\n");
  const dom: OverlayDomain = {
    description: "",
    body,
    include: [],
    exclude: [],
    unlisted: false,
    keywords: [],
    featured: [],
    notableCount: 0,
  };
  const unquote = (s: string): string => s.trim().replace(/^['"]|['"]$/g, "");
  const inlineList = (val: string): string[] => {
    const out: string[] = [];
    if (val.startsWith("[") && val.endsWith("]")) {
      for (const t of val.slice(1, -1).split(",")) {
        if (t.trim()) out.push(unquote(t));
      }
    }
    return out;
  };
  // indentOf returns the leading-space count of a line.
  const indentOf = (s: string): number => s.length - s.trimStart().length;
  // pending tracks which list a `- x` block item appends to. discovery tracks
  // whether the cursor is inside the nested discovery: block.
  let pending: "include" | "exclude" | "keywords" | "featured" | null = null;
  let inDiscovery = false;
  for (const raw of lines.slice(1, end)) {
    const trimmed = raw.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const indent = indentOf(raw);
    // A block list item appends to the pending list.
    if (pending && trimmed.startsWith("- ")) {
      const item = unquote(trimmed.slice(2));
      if (item) {
        if (pending === "include") dom.include.push(item);
        else if (pending === "exclude") dom.exclude.push(item);
        else if (pending === "keywords") dom.keywords.push(item);
        else dom.featured.push(item);
      }
      continue;
    }
    pending = null;
    const sep = raw.indexOf(":");
    if (sep === -1) continue;
    const key = raw.slice(0, sep).trim();
    const val = raw.slice(sep + 1).trim();
    if (indent === 0) {
      // A new top-level key closes the discovery: block.
      inDiscovery = false;
      if (key === "description") {
        dom.description = unquote(val);
      } else if (key === "unlisted") {
        dom.unlisted = unquote(val).toLowerCase() === "true";
      } else if (key === "include") {
        if (val) dom.include.push(...inlineList(val));
        else pending = "include";
      } else if (key === "exclude") {
        if (val) dom.exclude.push(...inlineList(val));
        else pending = "exclude";
      } else if (key === "discovery") {
        inDiscovery = true;
      }
      continue;
    }
    // Nested keys belong to the discovery: block.
    if (inDiscovery) {
      if (key === "keywords") {
        if (val) dom.keywords.push(...inlineList(val));
        else pending = "keywords";
      } else if (key === "featured") {
        if (val) dom.featured.push(...inlineList(val));
        else pending = "featured";
      } else if (key === "notable_count") {
        const n = parseInt(unquote(val), 10);
        if (Number.isFinite(n)) dom.notableCount = n;
      }
    }
  }
  return dom;
}

// match reports whether the §4.5.2 glob pattern matches the canonical artifact
// id. `*` matches one path segment, `**` matches zero or more segments
// (recursive), `{a,b,c}` matches any alternative. Mirrors pkg/domain.Match.
export function match(pattern: string, id: string): boolean {
  for (const alt of expandAlternatives(pattern)) {
    if (matchSegments(alt.split("/"), id.split("/"))) return true;
  }
  return false;
}

// matchAny reports whether any non-empty pattern matches id.
export function matchAny(patterns: string[], id: string): boolean {
  return patterns.some((p) => p !== "" && match(p, id));
}

// resolveImports computes the §4.5.2 import set: every id in ids matching an
// include pattern, with anything matching an exclude pattern removed (exclude
// after include). The result preserves the input order of ids.
export function resolveImports(include: string[], exclude: string[], ids: string[]): string[] {
  return ids.filter((id) => matchAny(include, id) && !matchAny(exclude, id));
}

// fallbackDescription synthesizes the §4.5.5 description fallback for a domain
// with no DOMAIN.md description: the directory basename, de-slugged (hyphens
// and underscores become spaces) and title-cased. Mirrors
// pkg/domain.FallbackDescription.
export function fallbackDescription(path: string): string {
  let base = path;
  const i = path.lastIndexOf("/");
  if (i >= 0) base = path.slice(i + 1);
  base = base.replace(/-/g, " ").replace(/_/g, " ");
  return base
    .split(/\s+/)
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

// globLiteralPrefix returns the leading literal path segments of a §4.5.2 glob,
// stopping at the first segment carrying a glob metacharacter. It scopes a
// catalog fetch to the slice an include pattern can match
// ("finance/ap/*" -> "finance/ap"); a leading glob yields "" (the whole
// catalog). Mirrors cmd/podium-mcp.globLiteralPrefix.
export function globLiteralPrefix(pattern: string): string {
  const out: string[] = [];
  for (const seg of pattern.split("/")) {
    if (/[*{},]/.test(seg)) break;
    out.push(seg);
  }
  return out.join("/");
}

// expandAlternatives expands a single `{a,b,c}` group into its alternatives,
// recursing so multiple groups all expand. A pattern with no group returns
// itself.
function expandAlternatives(pattern: string): string[] {
  const open = pattern.indexOf("{");
  if (open < 0) return [pattern];
  const rel = pattern.slice(open).indexOf("}");
  if (rel < 0) return [pattern];
  const closeIdx = rel + open;
  const prefix = pattern.slice(0, open);
  const suffix = pattern.slice(closeIdx + 1);
  const out: string[] = [];
  for (const choice of pattern.slice(open + 1, closeIdx).split(",")) {
    out.push(...expandAlternatives(prefix + choice + suffix));
  }
  return out;
}

// matchSegments matches pattern segments against target segments, honoring `*`
// (one segment) and `**` (zero or more segments).
function matchSegments(pat: string[], tgt: string[]): boolean {
  for (let i = 0; i < pat.length; i++) {
    const seg = pat[i];
    if (seg === "**") {
      if (i === pat.length - 1) return true;
      const rest = pat.slice(i + 1);
      for (let j = 0; j <= tgt.length; j++) {
        if (matchSegments(rest, tgt.slice(j))) return true;
      }
      return false;
    }
    if (i >= tgt.length) return false;
    if (seg !== "*" && seg !== tgt[i]) return false;
  }
  return pat.length === tgt.length;
}

// holdsManifest reports whether dir directly holds a regular file named name,
// following a symbolic link as filesystem.PackageManifests in
// pkg/registry/filesystem/discovery.go does with fs.Stat. Dirent.isFile()
// reports false for a link, so a SKILL.md linked from a skill shared across
// tools would otherwise go undiscovered. A link that cannot be resolved reads
// as absent, as os.path.isfile does in the Python overlay.
//
// Spec: §4.2
async function holdsManifest(
  fs: typeof import("node:fs/promises"),
  path: typeof import("node:path"),
  dir: string,
  entries: import("node:fs").Dirent[],
  name: string,
): Promise<boolean> {
  const entry = entries.find((e) => e.name === name);
  if (!entry) return false;
  if (entry.isFile()) return true;
  if (!entry.isSymbolicLink()) return false;
  try {
    return (await fs.stat(path.join(dir, name))).isFile();
  } catch {
    return false;
  }
}

// readOptional returns a file's text, or "" when it cannot be read.
async function readOptional(fs: typeof import("node:fs/promises"), file: string): Promise<string> {
  try {
    return await fs.readFile(file, "utf-8");
  } catch {
    return "";
  }
}

// collectResources returns every file under the package root dir, keyed by
// its package-relative path, other than the root files named in skip and the
// files of a nested package.
async function collectResources(
  fs: typeof import("node:fs/promises"),
  path: typeof import("node:path"),
  dir: string,
  skip: Set<string>,
): Promise<Record<string, string>> {
  const resources: Record<string, string> = {};
  const collect = async (sub: string): Promise<void> => {
    const entries = await fs.readdir(sub, { withFileTypes: true });
    // spec §4.4 — the files of a nested package are not resources of the
    // enclosing one, so the walk stops at a subdirectory that directly
    // contains an ARTIFACT.md. The test is the manifest alone, mirroring the
    // artifact result of filesystem.PackageManifests in
    // pkg/registry/filesystem/discovery.go, so a subdirectory holding only a
    // SKILL.md stays part of this package.
    if (sub !== dir && (await holdsManifest(fs, path, sub, entries, "ARTIFACT.md"))) return;
    for (const e of entries) {
      const full = path.join(sub, e.name);
      if (e.isDirectory()) {
        await collect(full);
        continue;
      }
      const rel = path.relative(dir, full).split(path.sep).join("/");
      if (skip.has(rel)) continue;
      resources[rel] = await readOptional(fs, full);
    }
  };
  await collect(dir);
  return resources;
}

// overlayArtifact assembles an overlay record and its BM25 tokens.
function overlayArtifact(
  id: string,
  fields: FrontmatterFields,
  frontmatter: string,
  body: string,
  skillRaw: string,
  resources: Record<string, string>,
): OverlayArtifact {
  const text = [id, fields.type, fields.description, fields.tags.join(" "), body].join(" ");
  return {
    id,
    type: fields.type,
    version: fields.version,
    description: fields.description,
    tags: fields.tags,
    frontmatter,
    body,
    skillRaw,
    resources,
    tokens: tokenize(text),
  };
}

// LocalOverlay indexes an overlay directory and ranks it with BM25 (§6.4.1).
export class LocalOverlay {
  // spec §6.4.1 — BM25 is the default overlay ranker (standard Okapi params).
  private static readonly K1 = 1.2;
  private static readonly B = 0.75;

  readonly artifacts = new Map<string, OverlayArtifact>();
  // §4.5.4 — every DOMAIN.md under the overlay keyed by canonical domain path
  // (the dir relative to the overlay root, "/"-joined; "" for the root level).
  readonly domains = new Map<string, OverlayDomain>();
  private avgLen = 0;
  private readonly docFreq = new Map<string, number>();

  private constructor() {}

  static async load(overlayPath: string): Promise<LocalOverlay> {
    const overlay = new LocalOverlay();
    if (!overlayPath) return overlay;
    const fs = await import("node:fs/promises");
    const path = await import("node:path");
    let exists = false;
    try {
      exists = (await fs.stat(overlayPath)).isDirectory();
    } catch {
      exists = false;
    }
    if (!exists) return overlay;

    // Walk for package directories and for directories that directly contain
    // a DOMAIN.md. The canonical key is the dir relative to the overlay root
    // ("/"-joined); the root level is keyed "". inPackage reports whether a
    // directory between dir and the overlay root is a package.
    //
    // Spec: §4.2 — a directory is a package when it directly holds
    // ARTIFACT.md, or when it directly holds SKILL.md and no directory between
    // it and the overlay root is a package. This mirrors loadPackage, which
    // combines filesystem.PackageManifests and filesystem.WithinPackage in
    // pkg/registry/filesystem.
    const walk = async (dir: string, inPackage: boolean): Promise<void> => {
      const entries = await fs.readdir(dir, { withFileTypes: true });
      const rel = path.relative(overlayPath, dir).split(path.sep).join("/");
      const key = rel === "." ? "" : rel;
      const hasArtifact = await holdsManifest(fs, path, dir, entries, "ARTIFACT.md");
      const hasSkill = await holdsManifest(fs, path, dir, entries, "SKILL.md");
      const isPackage = key !== "" && (hasArtifact || (hasSkill && !inPackage));
      if (isPackage) {
        const art = hasArtifact
          ? await overlay.readPackage(fs, path, dir, key)
          : await overlay.readPlainSkill(fs, path, dir, key);
        if (art) overlay.artifacts.set(key, art);
      }
      if (entries.some((e) => e.isFile() && e.name === "DOMAIN.md")) {
        try {
          const text = await fs.readFile(path.join(dir, "DOMAIN.md"), "utf-8");
          const dom = parseDomainFrontmatter(text);
          if (dom) overlay.domains.set(key, dom);
        } catch {
          // skip a malformed or unreadable DOMAIN.md
        }
      }
      for (const e of entries) {
        // spec §6.4 — an overlay carries the registry-side layers' format, so
        // discovery skips a dot-prefixed directory below the overlay root
        // together with everything under it, as walkLayer does in
        // pkg/registry/filesystem/walk.go. The root itself is exempt, because
        // the default overlay path is <CWD>/.podium/overlay/.
        if (e.isDirectory() && !e.name.startsWith(".")) await walk(path.join(dir, e.name), inPackage || isPackage);
      }
    };
    await walk(overlayPath, false);
    overlay.index();
    return overlay;
  }

  // readPackage loads a package whose root holds ARTIFACT.md. For a skill it
  // reads the sibling SKILL.md too, and an ARTIFACT.md that declares no
  // version takes the SKILL.md version (§4.3.4).
  private async readPackage(
    fs: typeof import("node:fs/promises"),
    path: typeof import("node:path"),
    dir: string,
    id: string,
  ): Promise<OverlayArtifact | null> {
    let frontmatter: string;
    try {
      frontmatter = await fs.readFile(path.join(dir, "ARTIFACT.md"), "utf-8");
    } catch {
      return null;
    }
    const { fields, body } = parseFrontmatter(frontmatter);
    if (fields.type !== "skill") {
      const resources = await collectResources(fs, path, dir, new Set(["ARTIFACT.md"]));
      return overlayArtifact(id, fields, frontmatter, body, "", resources);
    }
    const skillRaw = await readOptional(fs, path.join(dir, "SKILL.md"));
    fields.version = resolveSkillVersion(fields.version, metadataVersion(skillRaw));
    const resources = await collectResources(fs, path, dir, new Set(["ARTIFACT.md", "SKILL.md"]));
    return overlayArtifact(id, fields, frontmatter, body, skillRaw, resources);
  }

  // readPlainSkill loads a package whose root holds SKILL.md and no
  // ARTIFACT.md. The description, the search tokens, the body, and the tags
  // come from SKILL.md, the tags from a metadata.tags value in the readable
  // form, and the frontmatter is the synthesized document, which is never
  // parsed for a field.
  //
  // Spec: §4.3.4, §6.4
  private async readPlainSkill(
    fs: typeof import("node:fs/promises"),
    path: typeof import("node:path"),
    dir: string,
    id: string,
  ): Promise<OverlayArtifact | null> {
    let skillRaw: string;
    try {
      skillRaw = await fs.readFile(path.join(dir, "SKILL.md"), "utf-8");
    } catch {
      return null;
    }
    const { fields: skill, body } = parseFrontmatter(skillRaw);
    const version = resolveSkillVersion("", metadataVersion(skillRaw));
    const fields: FrontmatterFields = {
      type: "skill",
      version,
      description: skill.description,
      tags: metadataTags(skillRaw),
    };
    const resources = await collectResources(fs, path, dir, new Set(["SKILL.md"]));
    return overlayArtifact(id, fields, synthesizedSkillDocument(version), body, skillRaw, resources);
  }

  private index(): void {
    if (this.artifacts.size === 0) return;
    let total = 0;
    for (const art of this.artifacts.values()) {
      total += art.tokens.length;
      for (const term of new Set(art.tokens)) {
        this.docFreq.set(term, (this.docFreq.get(term) ?? 0) + 1);
      }
    }
    this.avgLen = total / this.artifacts.size;
  }

  private bm25(queryTokens: string[], art: OverlayArtifact): number {
    if (queryTokens.length === 0 || art.tokens.length === 0) return 0;
    const n = this.artifacts.size;
    const dl = art.tokens.length;
    const counts = new Map<string, number>();
    for (const t of art.tokens) counts.set(t, (counts.get(t) ?? 0) + 1);
    let score = 0;
    for (const term of queryTokens) {
      const tf = counts.get(term) ?? 0;
      if (tf === 0) continue;
      const df = this.docFreq.get(term) ?? 0;
      const idf = Math.log(1 + (n - df + 0.5) / (df + 0.5));
      const denom = tf + LocalOverlay.K1 * (1 - LocalOverlay.B + (LocalOverlay.B * dl) / (this.avgLen || 1));
      score += (idf * (tf * (LocalOverlay.K1 + 1))) / denom;
    }
    return score;
  }

  // spec §6.4: the overlay is the highest-precedence layer in the caller's
  // effective view, so a scoped query excludes overlay artifacts whose id
  // falls outside the requested domain path. The `scope` prefix match mirrors
  // the MCP server's overlay filter (cmd/podium-mcp/local_search.go).
  search(
    query: string,
    opts: { type?: string; scope?: string; tags?: string[]; topK?: number } = {},
  ): OverlayArtifact[] {
    const topK = opts.topK ?? 10;
    const wantTags = new Set(opts.tags ?? []);
    const candidates = [...this.artifacts.values()].filter(
      (a) =>
        (!opts.type || a.type === opts.type) &&
        (!opts.scope || a.id.startsWith(opts.scope)) &&
        (wantTags.size === 0 || [...wantTags].every((t) => a.tags.includes(t))),
    );
    if (query) {
      const q = tokenize(query);
      return candidates
        .map((a) => ({ score: this.bm25(q, a), a }))
        .filter((p) => p.score > 0)
        .sort((x, y) => y.score - x.score || (x.a.id < y.a.id ? -1 : 1))
        .slice(0, topK)
        .map((p) => p.a);
    }
    return candidates.sort((x, y) => (x.id < y.id ? -1 : 1)).slice(0, topK);
  }

  get(id: string): OverlayArtifact | undefined {
    return this.artifacts.get(id);
  }
}

// rrfFuse performs reciprocal rank fusion over ranked id lists (§6.4.1):
// the fused score of an id is the sum of 1 / (k + rank) across the lists
// that contain it (rank 0 is best).
export function rrfFuse(rankedLists: string[][], k = 60): Map<string, number> {
  const scores = new Map<string, number>();
  for (const ranked of rankedLists) {
    ranked.forEach((id, rank) => {
      scores.set(id, (scores.get(id) ?? 0) + 1 / (k + rank + 1));
    });
  }
  return scores;
}
