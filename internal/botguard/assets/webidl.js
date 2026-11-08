// Small binding mechanisms, not an IDL interpreter. The final family installer
// captures this temporary export and deletes it. No ordinary access calls Go.
(function () {
  const defineProperty = Object.defineProperty;
  const descriptors = Object.getOwnPropertyDescriptors;

  function domString(value) {
    if (typeof value === 'symbol') throw new TypeError('Symbol');
    return String(value);
  }
  function usvString(value) {
    const text = domString(value);
    let result = '';
    for (let i = 0; i < text.length; i++) {
      const code = text.charCodeAt(i);
      if (code >= 0xd800 && code <= 0xdbff) {
        const next = text.charCodeAt(i + 1);
        if (next >= 0xdc00 && next <= 0xdfff) result += text[i] + text[++i];
        else result += '\ufffd';
      } else result += code >= 0xdc00 && code <= 0xdfff ? '\ufffd' : text[i];
    }
    return result;
  }
  function nullable(value, convert) {
    return value === null || value === undefined ? null : convert(value);
  }
  // Presence is separate from value: no default parameters or destructuring.
  function argument(args, index) {
    const present = index < args.length;
    return {present, value:present ? args[index] : undefined};
  }
  function required(args, index, message) {
    if (index >= args.length) throw new TypeError(message);
    return args[index];
  }
  function defaulted(arg, fallback, undefinedUsesDefault) {
    return !arg.present || (undefinedUsesDefault && arg.value === undefined) ? fallback : arg.value;
  }
  // Only the 16/32-bit integer families needed by images and planned UI events.
  // Unary + rejects direct AND object-returned BigInt/Symbol, once, in order.
  function integer(value, bits, unsigned) {
    const number = +value;
    if (bits !== 16 && bits !== 32) throw new TypeError('Unsupported integer width');
    if (!Number.isFinite(number) || number === 0) return 0;
    const modulus = 2 ** bits;
    let result = Math.trunc(number) % modulus;
    if (result < 0) result += modulus;
    if (!unsigned && result >= modulus / 2) result -= modulus;
    return result === 0 ? 0 : result;
  }
  // Fast, equivalent 32-bit specializations. Interface range limits belong to
  // the caller; these helpers do not impose HTML content-attribute policies.
  function unsignedLong(value) { return (+value) >>> 0; }
  function long(value) { return (+value) | 0; }
  // No clamp/enforce-range/dictionary/float families until an adopted member
  // needs them. Callers can compose another numeric policy without dispatch.

  function receiver(check, message) {
    return function (value) {
      if (!check(value)) throw new TypeError(message);
      return value;
    };
  }
  function brandedReceiver(brands, message) {
    return receiver(value => brands.has(value), message);
  }
  function legacyInstanceofReceiver(Constructor, message) {
    return receiver(value => value instanceof Constructor, message);
  }
  // Descriptor policies are explicit input. Retain supplied functions verbatim
  // (name, length, constructibility), including legacy get/set naming.
  function accessor(target, name, descriptor) {
    defineProperty(target, name, descriptor);
  }
  function accessors(target, source) {
    Object.defineProperties(target, descriptors(source));
  }
  function method(target, name, fn, flags) {
    defineProperty(target, name, {value:fn,enumerable:flags.enumerable,
      configurable:flags.configurable,writable:flags.writable});
  }
  function readonly(target, name, get, flags) {
    accessor(target,name,{get,enumerable:flags.enumerable,configurable:flags.configurable});
  }
  // Explicit error policy: never pretend an ordinary Error is a DOMException.
  // A realm with a validated DOMException may supply its constructor. Legacy
  // callers can select namedError; M1 images continue throwing TypeError.
  function domException(Constructor, name, message) {
    return new Constructor(message, name);
  }
  function namedError(name, message) {
    const error = new Error(message);
    error.name = name;
    return error;
  }
  // prepare may read/convert several arguments in explicit order. Only commit
  // mutates state. This does not roll back user conversion side effects.
  function prepareThenCommit(prepare, commit) {
    const converted = prepare();
    return commit(converted);
  }
  globalThis.pocWebIDL = Object.freeze({domString,usvString,nullable,argument,required,
    defaulted,integer,unsignedLong,long,receiver,brandedReceiver,legacyInstanceofReceiver,
    accessor,accessors,method,readonly,domException,namedError,prepareThenCommit});
})();
