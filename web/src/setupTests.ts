import '@testing-library/jest-dom/vitest';

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

// jsdom exposes the element but does not implement the native dialog methods.
HTMLDialogElement.prototype.showModal ??= function () { this.setAttribute('open', ''); };
HTMLDialogElement.prototype.close ??= function () { this.removeAttribute('open'); };
