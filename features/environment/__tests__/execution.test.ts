import { acquireAppleAiSlot } from '../../ai-coach/appleAiExecution';
it('does not queue competing observers or let a stale release free the next owner', () => {
  const first = acquireAppleAiSlot()!;
  expect(acquireAppleAiSlot()).toBeNull();
  first();
  const second = acquireAppleAiSlot()!;
  first();
  expect(acquireAppleAiSlot()).toBeNull();
  second();
  const third = acquireAppleAiSlot();
  expect(third).not.toBeNull(); third!();
});
