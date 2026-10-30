// Fetch usually rejects on abort; native transports and older hosts may not.
// Settle the UI on its own deadline and fence late transport completions.
export function settleOnAbort<T>(read: Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const abort = (): void => {
      signal.removeEventListener('abort', abort);
      reject(signal.reason ?? new DOMException('Search cancelled', 'AbortError'));
    };
    if (signal.aborted) abort();
    else signal.addEventListener('abort', abort, { once: true });
    read.then(resolve, reject).finally(() => signal.removeEventListener('abort', abort));
  });
}
