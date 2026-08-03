import { ref } from 'vue';
import type { Ref } from 'vue';

export type ThemeMode = 'dark' | 'light';

export interface UseTheme {
  isDarkTheme: Ref<boolean>;
  toggle: () => void;
  init: () => void;
}

function applyTheme(dark: boolean) {
  const root = document.documentElement;
  if (dark) {
    root.classList.remove('light-theme');
  } else {
    root.classList.add('light-theme');
  }
}

function readSavedTheme(): ThemeMode | null {
  try {
    const saved = localStorage.getItem('theme');
    if (saved === 'light' || saved === 'dark') {
      return saved;
    }
  } catch (_err) {
    // ignore storage access errors
  }
  return null;
}

function writeTheme(mode: ThemeMode) {
  try {
    localStorage.setItem('theme', mode);
  } catch (_err) {
    // ignore storage write errors
  }
}

function prefersLight(): boolean {
  return Boolean(window.matchMedia?.('(prefers-color-scheme: light)').matches);
}

export function useTheme(): UseTheme {
  const isDarkTheme = ref(true);

  function init() {
    const saved = readSavedTheme();
    if (saved === 'light') {
      isDarkTheme.value = false;
    } else if (saved === 'dark') {
      isDarkTheme.value = true;
    } else {
      isDarkTheme.value = !prefersLight();
    }
    applyTheme(isDarkTheme.value);
  }

  function toggle() {
    isDarkTheme.value = !isDarkTheme.value;
    applyTheme(isDarkTheme.value);
    writeTheme(isDarkTheme.value ? 'dark' : 'light');
  }

  return { isDarkTheme, toggle, init };
}
