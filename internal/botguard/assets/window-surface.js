// A virtual window has no desktop placement: expose consistent zero coordinates.
// This layer has no CSS cascade/layout engine. Keep the callable Window API
// surface, but reject computation explicitly rather than fabricate CSS values.
(function () {
  // Window.name is a DOMString browsing-context name, initially empty.
  let contextName = '';
  Object.defineProperty(globalThis, 'name', {enumerable:true, configurable:true,
    get() { return contextName; },
    set(value) {
      if (typeof value === 'symbol') throw new TypeError('Symbol');
      contextName = String(value);
    }});
  // The virtual document owns the attached child frame elements. Keep this live,
  // rather than hardcoding zero after frames have been attached/removed.
  if (!('length' in globalThis))
    Object.defineProperty(globalThis, 'length', {enumerable:true, configurable:true,
      get() { return document.querySelectorAll('iframe,frame').length; }});
  if (!('frames' in globalThis))
    Object.defineProperty(globalThis, 'frames', {enumerable:true, configurable:true,
      get() { return globalThis; }});
  for (const name of ['screenX', 'screenY', 'screenLeft', 'screenTop']) {
    if (!(name in globalThis))
      Object.defineProperty(globalThis, name, {value: 0, writable: true, enumerable: true, configurable: true});
  }
  if (typeof globalThis.getComputedStyle !== 'function') {
    globalThis.getComputedStyle = function getComputedStyle(element) {
      if (!(element instanceof LD.Element))
        throw new TypeError('getComputedStyle requires an Element');
      const error = new Error('CSS style computation is not supported by the virtual window');
      error.name = 'NotSupportedError';
      throw error;
    };
  }
})();
