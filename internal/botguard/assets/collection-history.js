// LinkeDOM's NodeList and NamedNodeMap extend Array. Browser collection prototypes inherit
// Object.prototype instead. Preserve the existing array-backed implementation's
// methods for LinkeDOM internals while correcting the observable parent chain.
// Array.isArray(list) is still true: this is not a full WebIDL collection rewrite.
(function () {
  for (const proto of [LD.NodeList.prototype, Object.getPrototypeOf(document.createElement('img').attributes)]) {
   if (Object.getPrototypeOf(proto) === Array.prototype) {
    for (const key of Reflect.ownKeys(Array.prototype)) {
      if (key !== 'constructor' && key !== 'length' && !Object.prototype.hasOwnProperty.call(proto, key))
        Object.defineProperty(proto, key, Object.getOwnPropertyDescriptor(Array.prototype, key));
    }
    Object.setPrototypeOf(proto, Object.prototype);
   }
  }
  // The virtual window starts with one entry and no navigation engine. Expose
  // its read-only initial history state; no navigation success is fabricated.
  if (!('history' in globalThis)) {
    const history = Object.create(null);
    Object.defineProperties(history, {
      length: {enumerable:true, get(){return 1}},
      state: {enumerable:true, get(){return null}},
      [Symbol.toStringTag]: {value:'History'}
    });
    Object.defineProperty(globalThis,'history',{enumerable:true,configurable:true,get(){return history}});
  }
})();
