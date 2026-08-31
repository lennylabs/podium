// The appearance preference the account menu carries. The stylesheet reads
// the operating system setting on its own, and a data-theme attribute on the
// root element overrides it, so the preference is stamped there rather than
// branching any component on the theme.

import { useCallback, useEffect, useState } from 'react';

/** ThemePreference is what the account menu offers: the operating system
 * setting, or one of the two themes pinned. */
export type ThemePreference = 'system' | 'light' | 'dark';

/** storageKey is where the preference is persisted. It survives a reload
 * because a reader who pinned a theme once is not choosing it again. */
const storageKey = 'podium.theme';

export function readThemePreference(): ThemePreference {
  // A browser that refuses storage, which is what a private window can do,
  // leaves the page on the operating system setting rather than failing.
  try {
    const held = window.localStorage.getItem(storageKey);
    return held === 'light' || held === 'dark' ? held : 'system';
  } catch {
    return 'system';
  }
}

/** applyTheme stamps the preference on the root element. The system
 * preference carries no attribute, which is what returns the stylesheet to
 * prefers-color-scheme. */
export function applyTheme(preference: ThemePreference): void {
  const root = document.documentElement;
  if (preference === 'system') {
    root.removeAttribute('data-theme');
    return;
  }
  root.setAttribute('data-theme', preference);
}

/** useTheme holds the appearance preference and keeps the root element and
 * the stored value in step with it. */
export function useTheme(): [ThemePreference, (next: ThemePreference) => void] {
  const [preference, setPreference] = useState<ThemePreference>(() => readThemePreference());

  useEffect(() => {
    applyTheme(preference);
  }, [preference]);

  const choose = useCallback((next: ThemePreference) => {
    setPreference(next);
    try {
      window.localStorage.setItem(storageKey, next);
    } catch {
      // The preference still applies to this page; only its persistence is
      // lost, and a control that reported that would be reporting on the
      // browser rather than on the registry.
    }
  }, []);

  return [preference, choose];
}
