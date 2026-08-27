// Every surface owns its own request, and nothing a component renders
// fetches. This hook is that request's state: loading, the value, or the
// failure, plus the reload a retry control drives.

import { useCallback, useEffect, useState } from 'react';

export interface Async<T> {
  value: T | null;
  error: unknown;
  loading: boolean;
  reload: () => void;
}

export function useAsync<T>(run: () => Promise<T>, deps: unknown[]): Async<T> {
  const [value, setValue] = useState<T | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let live = true;
    setLoading(true);
    run().then(
      (next) => {
        if (live) {
          setValue(next);
          setError(null);
          setLoading(false);
        }
      },
      (err: unknown) => {
        if (live) {
          setValue(null);
          setError(err);
          setLoading(false);
        }
      },
    );
    return () => {
      live = false;
    };
    // run closes over the values the caller lists in deps, so the effect
    // re-runs on those and on a retry rather than on the closure's identity.
  }, [...deps, attempt]);

  const reload = useCallback(() => {
    setAttempt((n) => n + 1);
  }, []);
  return { value, error, loading, reload };
}

/** useErrorReport hands a catalog read's outcome to the shell, which is where
 * the catalog-scope rule is applied. It reports the successful outcome as
 * well, so a retry that answers clears the refused state the shell rendered.
 */
export function useErrorReport(error: unknown, report: (err: unknown) => void): void {
  useEffect(() => {
    report(error);
    // report is stable for the lifetime of the shell, so the outcome is
    // what the effect keys on.
  }, [error]);
}

/** useReachReport tells the shell that a read answered, which is the one
 * thing it says: the registry was reachable when it did. The layer surfaces
 * report through this rather than through the catalog outcome above, because
 * a layer endpoint resolves an unverifiable session to the anonymous caller
 * and answers, so its outcome carries nothing about the session and clearing
 * the refused state from it would state the session is live when it ended.
 * Spec: §13.10. */
export function useReachReport(reached: boolean, report: () => void): void {
  useEffect(() => {
    if (reached) {
      report();
    }
    // report is stable for the lifetime of the shell, so the transition into
    // a read that answered is what the effect keys on.
  }, [reached]);
}
