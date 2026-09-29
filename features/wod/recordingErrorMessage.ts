/** Native recording callbacks can contain a plain dictionary rather than Error. */
export function recordingErrorMessage(error: unknown): string {
  if (typeof error === "string") return error;
  if (error && typeof error === "object") {
    const { code, message, cause } = error as { code?: unknown; message?: unknown; cause?: unknown };
    const parts: string[] = [];
    if (typeof code === "string" || typeof code === "number") parts.push(`[${code}]`);
    if (typeof message === "string") parts.push(message);
    if (cause && typeof cause === "object" && "message" in cause && typeof cause.message === "string") {
      parts.push(`(${cause.message})`);
    }
    if (parts.length) return parts.join(" ");
    try { return JSON.stringify(error); } catch { return "unreadable_recording_error"; }
  }
  return String(error);
}
