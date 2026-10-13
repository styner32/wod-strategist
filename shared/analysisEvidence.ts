/** Display-only removal of machine evidence. The original stored response remains intact. */
export function withoutWorkoutEvidence(raw: string): string {
  const names = new Set(["workout_observations", "wod_evidence", "workout_progress"]);
  const lines = raw.replace(/\r\n/g, "\n").split("\n");
  const visible: string[] = [];
  for (let index = 0; index < lines.length; index++) {
    const fence = lines[index].match(/^\s{0,3}(`{3,}|~{3,})\s*([\w-]*)\s*$/);
    if (!fence) { visible.push(lines[index]); continue; }
    const closing = new RegExp(`^\\s{0,3}${fence[1][0]}{${fence[1].length},}\\s*$`);
    let end = index + 1;
    while (end < lines.length && !closing.test(lines[end])) {
      if (names.has(fence[2]) && /^\s*#{1,6}\s/.test(lines[end])) break;
      end++;
    }
    let hidden = names.has(fence[2]);
    if (fence[2] === "json") {
      try {
        const parsed: unknown = JSON.parse(lines.slice(index + 1, end).join("\n"));
        hidden = parsed != null && !Array.isArray(parsed) && typeof parsed === "object" && Object.keys(parsed).length > 0 && Object.keys(parsed).every(key => names.has(key));
      } catch { /* Unrecognized JSON remains inspectable in prose. */ }
    }
    const closed = end < lines.length && closing.test(lines[end]);
    if (!hidden) visible.push(...lines.slice(index, closed ? end + 1 : end));
    index = closed ? end : end - 1;
  }
  return visible.join("\n").trim();
}
