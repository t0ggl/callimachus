import '@testing-library/jest-dom';

// Default global fetch mock for tests using jsdom
if (!globalThis.fetch || typeof globalThis.fetch !== 'function') {
  // @ts-ignore
  globalThis.fetch = (() =>
    Promise.resolve({
      ok: true,
      json: () => Promise.resolve([]),
    })) as any;
} else {
  const originalFetch = globalThis.fetch;
  // @ts-ignore
  globalThis.fetch = ((url: any, ...args: any[]) => {
    if (typeof url === 'string' && url.startsWith('/')) {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve([]),
      });
    }
    return originalFetch(url, ...args);
  }) as any;
}
