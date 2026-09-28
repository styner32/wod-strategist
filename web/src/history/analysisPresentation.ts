import { parseHighlightTimestamp } from "./highlights";

const internalNames = new Set([
  "injury_timestamps",
  "highlights",
  "mobility",
  "score",
  "movements",
  "observed_signals",
  "target_person",
]);
function internalJSON(text: string): boolean {
  try {
    const value: unknown = JSON.parse(text);
    if (value && !Array.isArray(value) && typeof value === "object") {
      const keys = Object.keys(value);
      return keys.length > 0 && keys.every((key) => internalNames.has(key));
    }
    if (Array.isArray(value) && value.length > 0) {
      return value.every(
        (row) =>
          row &&
          typeof row === "object" &&
          (("joint" in row && "observation" in row && "assessable" in row) ||
            ("start" in row &&
              "end" in row &&
              "reason" in row &&
              [
                "positive_form",
                "form_issue",
                "fatigue_onset",
                "technique_event",
                "best_form",
                "worst_form",
                "mixed_form",
              ].includes(row.type))),
      );
    }
  } catch {
    /* Unknown or malformed generic JSON belongs to the original prose. */
  }
  return false;
}

// Display-only extraction: never modify the stored output. An unclosed internal
// fence stops at the first prose boundary, so subsequent analysis survives.
export function analysisProse(raw: string): string {
  const lines = raw.replace(/\r\n/g, "\n").split("\n");
  const out: string[] = [];
  for (let i = 0; i < lines.length; i++) {
    const fence = lines[i].match(/^\s{0,3}(`{3,}|~{3,})\s*([\w-]*)\s*$/);
    const xml = lines[i].match(/^\s*<([a-z_]+)>\s*(.*)$/);
    if (xml && internalNames.has(xml[1])) {
      const closing = `</${xml[1]}>`;
      if (lines[i].includes(closing)) continue;
      let j = i + 1;
      while (j < lines.length && !lines[j].includes(closing)) {
        if (
          /^\s*#{1,6}\s/.test(lines[j]) ||
          (lines[j - 1].trim() === "" && /^[\p{L}]/u.test(lines[j].trim()))
        )
          break;
        j++;
      }
      i = j < lines.length && lines[j].includes(closing) ? j : j - 1;
      continue;
    }
    if (!fence) {
      out.push(lines[i]);
      continue;
    }
    const marker = fence[1][0];
    const closing = new RegExp(`^\\s{0,3}${marker}{${fence[1].length},}\\s*$`);
    let j = i + 1;
    while (j < lines.length && !closing.test(lines[j])) {
      if (
        internalNames.has(fence[2]) &&
        (/^\s*#{1,6}\s/.test(lines[j]) ||
          (lines[j - 1].trim() === "" && /^[\p{L}]/u.test(lines[j].trim())))
      )
        break;
      j++;
    }
    const body = lines.slice(i + 1, j).join("\n");
    const closed = j < lines.length && closing.test(lines[j]);
    if (
      !internalNames.has(fence[2]) &&
      !(fence[2] === "json" && internalJSON(body))
    ) {
      out.push(...lines.slice(i, closed ? j + 1 : j));
    }
    i = closed ? j : j - 1;
  }
  return out
    .join("\n")
    .replace(/(?:\n\s*---\s*){2,}/g, "\n\n---\n")
    .replace(/\n{4,}/g, "\n\n\n")
    .trim();
}

export interface AnalysisSegment {
  title: string;
  start: number;
  end: number;
  body: string;
}
export function analysisSegments(raw: string): AnalysisSegment[] {
  const lines = analysisProse(raw).split("\n");
  const sections: AnalysisSegment[] = [];
  let fence: string | null = null;
  let current: AnalysisSegment | null = null;
  for (const line of lines) {
    const marker = line.match(/^\s{0,3}(`{3,}|~{3,})/);
    if (marker) {
      if (!fence) fence = marker[1];
      else if (marker[1][0] === fence[0] && marker[1].length >= fence.length && /^\s{0,3}(`{3,}|~{3,})\s*$/.test(line))
        fence = null;
      if (current) current.body += `${line}\n`;
      continue;
    }
    const heading =
      !fence &&
      line.match(/^## 세그먼트 \d+: (.+) \(([\d:.]+) ~ ([\d:.]+)\)\s*$/);
    if (heading) {
      const start = parseHighlightTimestamp(heading[2]);
      const end = parseHighlightTimestamp(heading[3]);
      current =
        start !== null && end !== null && end > start
          ? { title: heading[1], start, end, body: "" }
          : null;
      if (current) sections.push(current);
    } else if (current) current.body += `${line}\n`;
  }
  return sections;
}
export function legacyOverallSummary(raw: string): string | null {
  try {
    const parsed = JSON.parse(raw);
    return typeof parsed?.overall_summary === "string"
      ? parsed.overall_summary
      : null;
  } catch {
    return null;
  }
}
