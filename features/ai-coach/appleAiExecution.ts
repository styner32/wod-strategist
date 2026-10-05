/** A non-queuing slot shared by posture, environment and explicit reanalysis. */
let owner: symbol | null = null;
export function acquireAppleAiSlot(): (() => void) | null {
  if (owner) return null;
  const token = Symbol();
  owner = token;
  return () => { if (owner === token) owner = null; };
}
