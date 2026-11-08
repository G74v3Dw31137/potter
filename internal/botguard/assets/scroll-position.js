// LinkeDOM has no layout/scroll engine. Model the numeric scrollLeft state exposed
// by jsdom's Element implementation: a fresh element starts at 0. This is not a
// renderer, and does not claim to implement CSSOM scrolling/clamping/layout.
(function () {
  for (const name of ['scrollLeft','scrollTop']) {
  if (name in LD.Element.prototype) continue;
  const positions = new WeakMap();
  Object.defineProperty(LD.Element.prototype, name, {
    configurable: true,
    enumerable: true,
    get() { if (!(this instanceof LD.Element)) throw new TypeError('Illegal invocation'); return positions.has(this) ? positions.get(this) : 0; },
    set(value) { if (!(this instanceof LD.Element)) throw new TypeError('Illegal invocation'); positions.set(this, +value); }
  });
  }
})();
