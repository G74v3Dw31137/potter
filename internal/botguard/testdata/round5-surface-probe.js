// Public deterministic probe, shared unchanged with the pinned jsdom reference.
(function(w) {
  const assert = (value, label) => { if (!value) throw Error(label); };
  const throws = (fn, label) => {
    let ok = false;
    try { fn(); } catch (e) { ok = e instanceof w.TypeError; }
    assert(ok, label);
  };
  const img = w.document.createElement('img');
  for (const target of [w.Node, w.Node.prototype]) {
    const d = Object.getOwnPropertyDescriptor(target, 'PROCESSING_INSTRUCTION_NODE');
    assert(d.value === 7 && d.enumerable && !d.writable && !d.configurable, 'constant descriptor');
  }
  assert(img.PROCESSING_INSTRUCTION_NODE === 7 && !Object.hasOwn(img, 'PROCESSING_INSTRUCTION_NODE'), 'inherited constant');
  const d = Object.getOwnPropertyDescriptor(w.Element.prototype, 'getAttributeNodeNS');
  assert(d.value.length === 2 && d.writable && d.enumerable && d.configurable, 'lookup descriptor');
  const get = d.value;
  assert(get.call(img, null, 'id') === null, 'missing attribute');
  img.setAttribute('id', 'first');
  const attr = img.getAttributeNode('id');
  assert(attr !== null, 'attribute exists');
  for (const ns of [null, undefined, ''])
    assert(img.getAttributeNodeNS(ns, 'id') === attr, 'attribute identity');
  assert(img.getAttributeNodeNS('urn:other', 'id') === null, 'namespace must match');
  assert(img.getAttributeNodeNS(null, 'ID') === null, 'case sensitive local name');
  assert(img.getAttributeNodeNS(null, {toString() {return 'id';}}) === attr, 'name conversion');
  assert(img.getAttributeNodeNS({toString() {return '';}}, 'id') === attr, 'namespace conversion');
  attr.value = 'second';
  assert(img.getAttributeNodeNS(null, 'id').value === 'second', 'live Attr');
  img.removeAttribute('id');
  assert(img.getAttributeNodeNS(null, 'id') === null, 'removed Attr');
  img.setAttribute('id', 'third');
  assert(img.getAttributeNodeNS(null, 'id') !== attr, 'replacement identity');
  const clone = img.cloneNode();
  assert(clone.getAttributeNodeNS(null, 'id') !== img.getAttributeNodeNS(null, 'id'), 'clone identity');
  assert(clone.getAttributeNodeNS(null, 'id').value === 'third', 'clone value');
  const svg = w.document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('Case', 'yes');
  assert(svg.getAttributeNodeNS(null, 'Case') === svg.getAttributeNode('Case'), 'SVG case preserved');
  assert(svg.getAttributeNodeNS(null, 'case') === null, 'SVG case sensitive');
  throws(() => get.call({}, null, 'id'), 'lookup receiver');
  throws(() => get.call(img, null), 'lookup required args');
  throws(() => get.call(img, Symbol(), 'id'), 'namespace Symbol');
  throws(() => get.call(img, null, Symbol()), 'name Symbol');
  const order = [];
  assert(get.call(img, {toString() {order.push('ns'); return '';}},
    {toString() {order.push('local'); return 'id';}}) === img.getAttributeNode('id'), 'argument conversion');
  assert(order.join() === 'ns,local', 'conversion order');

  const url = Object.getOwnPropertyDescriptor(w.HTMLImageElement.prototype, 'longDesc');
  assert(url.get && url.set && url.enumerable && url.configurable, 'longDesc descriptor');
  assert(img.longDesc === '', 'longDesc absent');
  const root = w.document.URL.replace(/\/$/, '');
  for (const [value, expected] of [
    ['', root + '/'], ['help/info', root + '/help/info'], ['/info', root + '/info'],
    ['../info', root + '/info'], ['#part', root + '/#part'],
    ['?q=a b', root + '/?q=a%20b'], ['//example.test/a', 'https://example.test/a'],
    ['HTTPS://EXAMPLE.TEST:443', 'https://example.test/'],
    ['https://example.test/a b', 'https://example.test/a%20b'],
    ['https://example.test/\u0430', 'https://example.test/%D0%B0'],
    ['https://[bad', 'https://[bad'], ['data:text/plain,hello', 'data:text/plain,hello'],
    [null, root + '/null'], [undefined, root + '/undefined'], [12n, root + '/12'],
    ['\ud800', root + '/%EF%BF%BD'], ['\ud83d\ude00', root + '/%F0%9F%98%80']
  ]) {
    img.longDesc = value;
    assert(img.longDesc === expected, 'longDesc resolution ' + String(value));
  }
  assert(img.getAttribute('longdesc') === '\ud83d\ude00', 'USVString pair');
  img.longDesc = '\ud800';
  assert(img.getAttribute('longdesc') === '\ufffd', 'USVString replacement');
  img.longDesc = {toString() {return 'relative';}};
  assert(img.getAttribute('longdesc') === 'relative', 'raw attribute');
  assert(img.cloneNode().longDesc === root + '/relative', 'longDesc clone');
  img.setAttribute('longdesc', '/changed');
  assert(img.longDesc === root + '/changed', 'longDesc content mutation');
  img.removeAttribute('longdesc');
  assert(img.longDesc === '', 'longDesc removal');
  throws(() => url.get.call({}), 'longDesc receiver');
  throws(() => url.set.call({}, 'x'), 'longDesc setter receiver');
  throws(() => {img.longDesc = Symbol();}, 'longDesc Symbol');
  const base = w.document.createElement('base');
  base.setAttribute('href', '/docs/');
  w.document.head.appendChild(base);
  img.longDesc = 'description';
  assert(img.longDesc === root + '/docs/description', 'base resolution');
  base.setAttribute('href', 'https://example.test/new/');
  // jsdom 29.1.1 caches this getter across base-only mutations. Change the
  // attribute too in the shared probe; Go has a separate base-only assertion.
  img.longDesc = 'updated';
  assert(img.longDesc === 'https://example.test/new/updated', 'base mutation');
  base.remove();
  img.longDesc = 'removed';
  assert(img.longDesc === root + '/removed', 'base removal');
})(globalThis);
