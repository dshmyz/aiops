import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest';
import { useTheme } from './useTheme';

describe('useTheme', () => {
  let matchMediaMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    // 模拟空 localStorage
    Object.defineProperty(window, 'localStorage', {
      value: {
        getItem: vi.fn(() => null),
        setItem: vi.fn(),
        removeItem: vi.fn(),
        clear: vi.fn(),
        key: vi.fn(),
        length: 0,
      },
      writable: true,
      configurable: true,
    });
    matchMediaMock = vi.fn();
    Object.defineProperty(window, 'matchMedia', {
      value: matchMediaMock,
      writable: true,
      configurable: true,
    });
    document.documentElement.classList.remove('light-theme');
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  test('init 默认使用暗色主题当无保存且无 prefers-light', () => {
    matchMediaMock.mockReturnValue({ matches: false });
    const { isDarkTheme, init } = useTheme();
    init();
    expect(isDarkTheme.value).toBe(true);
    expect(document.documentElement.classList.contains('light-theme')).toBe(false);
  });

  test('init 当 prefers-color-scheme: light 时使用浅色主题', () => {
    matchMediaMock.mockReturnValue({ matches: true });
    const { isDarkTheme, init } = useTheme();
    init();
    expect(isDarkTheme.value).toBe(false);
    expect(document.documentElement.classList.contains('light-theme')).toBe(true);
  });

  test('init 当 localStorage 保存 light 时使用浅色主题', () => {
    (window.localStorage.getItem as ReturnType<typeof vi.fn>).mockReturnValue('light');
    matchMediaMock.mockReturnValue({ matches: false });
    const { isDarkTheme, init } = useTheme();
    init();
    expect(isDarkTheme.value).toBe(false);
    expect(document.documentElement.classList.contains('light-theme')).toBe(true);
  });

  test('init 当 localStorage 保存 dark 时使用暗色主题', () => {
    (window.localStorage.getItem as ReturnType<typeof vi.fn>).mockReturnValue('dark');
    matchMediaMock.mockReturnValue({ matches: true });
    const { isDarkTheme, init } = useTheme();
    init();
    expect(isDarkTheme.value).toBe(true);
    expect(document.documentElement.classList.contains('light-theme')).toBe(false);
  });

  test('toggle 从暗色切到浅色', () => {
    matchMediaMock.mockReturnValue({ matches: false });
    const { isDarkTheme, init, toggle } = useTheme();
    init();
    expect(isDarkTheme.value).toBe(true);

    toggle();

    expect(isDarkTheme.value).toBe(false);
    expect(document.documentElement.classList.contains('light-theme')).toBe(true);
    expect(window.localStorage.setItem).toHaveBeenCalledWith('theme', 'light');
  });

  test('toggle 从浅色切到暗色', () => {
    (window.localStorage.getItem as ReturnType<typeof vi.fn>).mockReturnValue('light');
    matchMediaMock.mockReturnValue({ matches: true });
    const { isDarkTheme, init, toggle } = useTheme();
    init();
    expect(isDarkTheme.value).toBe(false);

    toggle();

    expect(isDarkTheme.value).toBe(true);
    expect(document.documentElement.classList.contains('light-theme')).toBe(false);
    expect(window.localStorage.setItem).toHaveBeenCalledWith('theme', 'dark');
  });

  test('init 容忍 localStorage 访问失败', () => {
    Object.defineProperty(window, 'localStorage', {
      get() {
        throw new Error('storage disabled');
      },
      configurable: true,
    });
    matchMediaMock.mockReturnValue({ matches: false });
    const { isDarkTheme, init } = useTheme();
    expect(() => init()).not.toThrow();
    expect(isDarkTheme.value).toBe(true);
  });

  test('toggle 容忍 localStorage 写入失败', () => {
    Object.defineProperty(window, 'localStorage', {
      value: {
        getItem: vi.fn(() => null),
        setItem: vi.fn(() => {
          throw new Error('quota exceeded');
        }),
        removeItem: vi.fn(),
        clear: vi.fn(),
        key: vi.fn(),
        length: 0,
      },
      writable: true,
      configurable: true,
    });
    matchMediaMock.mockReturnValue({ matches: false });
    const { isDarkTheme, init, toggle } = useTheme();
    init();
    expect(() => toggle()).not.toThrow();
    expect(isDarkTheme.value).toBe(false);
  });
});
