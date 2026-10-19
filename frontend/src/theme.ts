import { useSyncExternalStore } from 'react';

// Tiny subscription around the theme toggle in App.tsx: components that need
// the current theme (CodeMirror's dark chrome) can't reach App's state, so
// App notifies and they re-read localStorage through this store.
const listeners = new Set<() => void>();

export function notifyThemeChange() {
  listeners.forEach((l) => l());
}

export function useIsDark(): boolean {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => {
        listeners.delete(cb);
      };
    },
    () => localStorage.getItem('fx.theme') === 'dark',
  );
}
