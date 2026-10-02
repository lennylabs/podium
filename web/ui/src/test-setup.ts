// Testing Library configuration shared by every UI test file. It is loaded
// through the setupFiles entry in vitest.config.ts, ahead of each file.

import { configure, getConfig } from '@testing-library/react';

/** postTask is the queue React's scheduler posts its work to. The scheduler
 * picks setImmediate where the runtime has one, which Node does, so a task
 * posted here runs after every scheduler task already waiting. */
const postTask: (task: () => void) => void =
  (globalThis as { setImmediate?: (task: () => void) => unknown }).setImmediate ??
  ((task) => {
    setTimeout(task, 0);
  });

function nextTask(): Promise<void> {
  return new Promise((resolve) => {
    postTask(resolve);
  });
}

const reactAsyncWrapper = getConfig().asyncWrapper;

configure({
  // A stubbed fetch that resolves outside act() commits through React's
  // scheduler, and on a loaded runner a chain of such reads (the session read,
  // then a read the rendered surface issues) can take longer than the default
  // one-second wait. The longer wait only extends a wait that would otherwise
  // fail, so a passing case takes as long as it did before.
  asyncUtilTimeout: 5000,
  // A findBy or waitFor resolves on the commit that drew what it was waiting
  // for. When that commit came from a resolved fetch rather than from an event
  // inside act(), React runs the commit's passive effects in a later scheduler
  // task. Testing Library's own wrapper drains a setTimeout(0) before it
  // returns, and on a loaded runner that timer fires ahead of the scheduler's
  // task. The case then continues while the effects are still pending: a
  // listener the menu or the dialog attaches is missing, a focus handoff has
  // not happened, and an event the case fires next is processed after the
  // pending effects, so a mount effect that closes a menu undoes the click
  // that opened it. Waiting out the scheduler's queued tasks before returning
  // hands the case a page whose effects have run. The second task covers a
  // scheduler pass that yielded and reposted its remaining work.
  asyncWrapper: (cb) =>
    reactAsyncWrapper(async () => {
      const result = await cb();
      await nextTask();
      await nextTask();
      return result;
    }),
});
