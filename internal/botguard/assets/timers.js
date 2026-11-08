// Intervals use the same bounded, session-owned scheduler and handle pool as
// timeouts. No background event loop or timer draining is introduced.
(function(schedule, cancel) {
  delete globalThis.pocSetInterval;
  globalThis.setInterval = function setInterval(handler, timeout = 0, ...args) {
    // DOMString conversion rejects Symbols, unlike String(Symbol()).
    if (typeof handler !== 'function') handler = `${handler}`;
    // WebIDL long conversion: ToNumber, truncation and signed 32-bit wrapping.
    return schedule(handler, (+timeout) | 0, ...args);
  };
  globalThis.clearInterval = function clearInterval(handle = 0) {
    return cancel((+handle) | 0);
  };
})(pocSetInterval, clearTimeout);
