// Public synthetic probe, executed unchanged in Goja and pinned offline jsdom.
(function(w) {
  'use strict';
  const assert = (value, label) => { if (!value) throw Error(label); };
  const throws = (fn, label) => {
    let error;
    try { fn(); } catch (e) { error = e; }
    assert(error instanceof w.TypeError, label);
  };
  const imageProto = w.HTMLImageElement.prototype;
  const complete = Object.getOwnPropertyDescriptor(imageProto, 'complete');
  const border = Object.getOwnPropertyDescriptor(imageProto, 'border');
  const which = Object.getOwnPropertyDescriptor(w.UIEvent.prototype, 'which');
  for (const [name, descriptor, setter] of [
    ['complete', complete, false], ['border', border, true], ['which', which, false]
  ]) {
    assert(descriptor.get && !!descriptor.set === setter && descriptor.enumerable &&
      descriptor.configurable && !('value' in descriptor), name + ' descriptor');
    assert(descriptor.get.name === 'get ' + name && descriptor.get.length === 0,
      name + ' getter name/arity');
    throws(() => new descriptor.get(), name + ' getter not constructible');
    throws(() => descriptor.get.call({}), name + ' receiver');
    throws(() => descriptor.get.apply(null, []), name + ' null receiver');
  }
  assert(border.set.name === 'set border' && border.set.length === 1, 'border setter metadata');
  const img = w.document.createElement('img');
  assert(Object.getPrototypeOf(img) === imageProto && img.constructor === w.HTMLImageElement,
    'image prototype and constructor');
  assert(img.complete === true && img.border === '' && !img.hasAttribute('border'), 'image defaults');
  assert(!Object.hasOwn(img, 'complete') && !Object.hasOwn(img, 'border'), 'inherited image accessors');
  throws(() => {img.complete = false;}, 'complete read only');
  assert(complete.get.call(img) === true && complete.get.apply(img, []) === true, 'complete call/apply');
  for (const src of ['', 'pending.png', '/pending.png', ' ', 'https://[invalid', 'data:image/png,']) {
    img.setAttribute('src', src);
    assert(img.complete === (src === ''), 'source availability ' + src);
    assert(img.cloneNode().complete === img.complete, 'clone source availability');
  }
  img.removeAttribute('src');
  assert(img.complete === true, 'source removal');
  w.document.body.appendChild(img);
  img.src = 'pending.png';
  assert(img.complete === false, 'attached source still unavailable');
  img.src = '';
  assert(img.complete === true, 'empty source property');
  img.remove();

  for (const [value, expected] of [
    [null, ''], [undefined, 'undefined'], [false, 'false'], [12n, '12'],
    [-2.5, '-2.5'], ['001px', '001px'], ['\ud800', '\ud800']
  ]) {
    img.border = value;
    assert(img.border === expected && img.getAttribute('border') === expected, 'border conversion');
    assert(img.cloneNode().border === expected, 'border clone');
  }
  let conversions = 0;
  border.set.call(img, {toString() { conversions++; return 'wide'; }});
  assert(conversions === 1 && border.get.apply(img, []) === 'wide', 'border call/apply conversion');
  throws(() => {img.border = Symbol();}, 'border Symbol');
  assert(img.border === 'wide', 'failed conversion preserves border');
  throws(() => border.set.call({}, {toString() { throw Error('must not convert'); }}),
    'border receiver before conversion');
  const failure = Error('conversion');
  let caught;
  try { img.border = {toString() { throw failure; }}; } catch (e) { caught = e; }
  assert(caught === failure && img.border === 'wide', 'conversion exception propagates');
  img.setAttribute('border', 'attribute');
  assert(img.border === 'attribute', 'live border reflection');
  const clone = img.cloneNode();
  img.removeAttribute('border');
  assert(img.border === '' && clone.border === 'attribute', 'border removal/clone independence');

  assert(!Object.hasOwn(w.MouseEvent.prototype, 'which'), 'which inherited from UIEvent');
  const legacy = w.document.createEvent('MouseEvents');
  assert(legacy instanceof w.MouseEvent && legacy instanceof w.UIEvent && legacy instanceof w.Event,
    'legacy event prototype chain');
  assert(legacy.which === 0 && !Object.hasOwn(legacy, 'which'), 'legacy which default');
  legacy.initMouseEvent('click', true, true, null, 2, 3, 4, 5, 6,
    false, false, false, false, 2, null);
  assert(legacy.which === 0, 'initMouseEvent retains synthetic which');
  for (const Constructor of [w.UIEvent, w.MouseEvent]) {
    for (const [value, expected] of [
      [undefined, 0], [null, 0], [NaN, 0], [Infinity, 0], [-1, 4294967295],
      [4294967297, 1], [3.9, 3], ['12', 12]
    ]) {
      const event = new Constructor('probe', {which: value});
      assert(event.which === expected && which.get.call(event) === expected &&
        which.get.apply(event, []) === expected, 'which unsigned long');
      throws(() => {event.which = 17;}, 'which read only');
      event.initUIEvent('other', false, false, null, 8);
      assert(event.which === expected && event.detail === 8, 'initUIEvent preserves which');
    }
    let reads = 0, numbers = 0;
    const event = new Constructor('probe', {
      get which() { reads++; return {valueOf() {numbers++; return 7;}}; }
    });
    assert(reads === 1 && numbers === 1 && event.which === 7, 'which converted once');
    caught = undefined;
    try {
      event.initUIEvent('other', false, false, w, {valueOf() {throw failure;}});
    } catch (e) { caught = e; }
    assert(caught === failure && event.view === null && event.which === 7,
      'failed detail conversion preserves view and which');
    throws(() => new Constructor('probe', {which: Symbol()}), 'which Symbol');
    throws(() => new Constructor('probe', {which: 1n}), 'which BigInt');
  }
})(globalThis);
