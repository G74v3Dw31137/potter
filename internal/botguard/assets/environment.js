var missingGlobalNames = /* @__PURE__ */ new Set();
Object.setPrototypeOf(globalThis, new Proxy(Object.getPrototypeOf(globalThis), { get: function(t, k, r) {
  if (!(k in t) && !missingGlobalNames.has(k)) {
    missingGlobalNames.add(k);
    print("global missing", String(k));
  }
  return Reflect.get(t, k, r);
}, has: function(t, k) {
  if (!(k in t) && !missingGlobalNames.has(k)) {
    missingGlobalNames.add(k);
    print("global missing", String(k));
  }
  return Reflect.has(t, k);
} }));
var window = globalThis, self = globalThis;
var performance = { now: performanceNow, timeOrigin: Date.now() };
var TextEncoder = class {
  encode(s = "") {
    return new Uint8Array(encodeUTF8(String(s)));
  }
};
var console = { log: print, warn: print, error: print };
var pageHref=typeof pocPageURL==="string"?pocPageURL:"https://www.youtube.com/";var pageHost=pageHref.split("/")[2];
var location = { href: pageHref, origin: "https://"+pageHost, protocol: "https:", host: pageHost, hostname: pageHost, pathname: "/", search: "", hash: "" };
var origin = location.origin;
var document = { URL: location.href, documentURI: location.href, location, defaultView: window, referrer: location.href, compatMode: "CSS1Compat", contentType: "text/html", characterSet: "UTF-8", readyState: "complete", cookie: "", hidden: true, visibilityState: "prerender", createElement: function(name) {
  print("createElement", name);
  var attrs = {};
  var el = { style: {}, tagName: name.toUpperCase(), height: 0, width: 0, naturalHeight: 0, naturalWidth: 0, complete: true, removeAttributeNode: function(attr) {
    if (!attr || !Object.prototype.hasOwnProperty.call(attrs, attr.name)) throw new Error("NotFoundError");
    delete attrs[attr.name];
    return attr;
  }, hasAttribute: function(k) {
    return Object.prototype.hasOwnProperty.call(attrs, k);
  }, removeAttributeNS: function(ns, k) {
    if (ns == null || ns === "") delete attrs[k];
  }, getAttribute: function(k) {
    return Object.prototype.hasOwnProperty.call(attrs, k) ? attrs[k] : null;
  }, removeAttribute: function(k) {
    delete attrs[k];
  }, setAttribute: function(k, v) {
    print("element attribute", k, String(v));
    attrs[String(k).toLowerCase()] = String(v);
  }, remove: function() {
  } };
  Object.defineProperties(el, { height: { get: function() {
    return Math.max(0, parseInt(attrs.height, 10) || 0);
  }, set: function(v) {
    attrs.height = String(v >>> 0);
  }, configurable: true }, width: { get: function() {
    return Math.max(0, parseInt(attrs.width, 10) || 0);
  }, set: function(v) {
    attrs.width = String(v >>> 0);
  }, configurable: true }, id: { get: function() {
    return attrs.id || "";
  }, set: function(v) {
    attrs.id = String(v);
  }, configurable: true }, className: { get: function() {
    return attrs["class"] || "";
  }, set: function(v) {
    attrs["class"] = String(v);
  }, configurable: true } });
  if (name === "iframe") {
    var frame = {};
    for (var key of ["String", "Object", "Array", "Function", "Number", "Boolean", "Date", "Math", "JSON", "RegExp", "Error", "TypeError", "RangeError", "SyntaxError", "ReferenceError", "URIError", "EvalError", "Symbol", "BigInt", "Map", "Set", "WeakMap", "WeakSet", "Promise", "Reflect", "Proxy", "ArrayBuffer", "DataView", "Int8Array", "Uint8Array", "Uint8ClampedArray", "Int16Array", "Uint16Array", "Int32Array", "Uint32Array", "Float32Array", "Float64Array", "BigInt64Array", "BigUint64Array", "parseInt", "parseFloat", "isNaN", "isFinite", "eval", "decodeURIComponent", "encodeURIComponent", "Infinity", "NaN", "undefined"]) {
      if (key in globalThis) frame[key] = globalThis[key];
    }
    el.contentWindow = new Proxy(frame, { get: function(t, k) {
      print("iframe window get", String(k));
      return t[k];
    } });
  }
  return new Proxy(el, { get: function(t, k) {
    if (!(k in t)) print("element missing", String(k));
    return t[k];
  } });
} };
var navigator = { userAgent: typeof pocUserAgent==="string"?pocUserAgent:"Mozilla/5.0", platform: "", language: "en-US", languages: ["en-US", "en"], cookieEnabled: true, hardwareConcurrency: 1 };
function eventTarget(o) {
  var listeners = {};
  o.addEventListener = function(type, fn) {
    (listeners[type] || (listeners[type] = [])).push(fn);
  };
  o.removeEventListener = function(type, fn) {
    listeners[type] = (listeners[type] || []).filter(function(x) {
      return x !== fn;
    });
  };
  o.dispatchEvent = function(e) {
    for (var fn of (listeners[e.type] || []).slice()) fn.call(o, e);
    return !e.defaultPrevented;
  };
  return o;
}
function domNode(tag) {
  return eventTarget({ tagName: tag.toUpperCase(), nodeName: tag.toUpperCase(), nodeType: 1, style: {}, childNodes: [], appendChild: function(el) {
    el.parentNode = this;
    this.childNodes.push(el);
    return el;
  }, removeChild: function(el) {
    var i = this.childNodes.indexOf(el);
    if (i < 0) throw new Error("NotFoundError");
    this.childNodes.splice(i, 1);
    el.parentNode = null;
    return el;
  }, clientWidth: 0, clientHeight: 0, offsetWidth: 0, offsetHeight: 0, getBoundingClientRect: function() {
    return { x: 0, y: 0, top: 0, left: 0, right: 0, bottom: 0, width: 0, height: 0 };
  } });
}
document.createEvent = function(name) {
  print("createEvent", name);
  if (!["Event", "Events", "HTMLEvents", "MouseEvent", "MouseEvents", "CustomEvent", "UIEvents"].includes(name)) throw new Error("NotSupportedError");
  return { type: "", bubbles: false, cancelable: false, defaultPrevented: false, initEvent: function(t, b, c) {
    this.type = t;
    this.bubbles = !!b;
    this.cancelable = !!c;
  }, preventDefault: function() {
    if (this.cancelable) this.defaultPrevented = true;
  }, stopPropagation: function() {
  }, initMouseEvent: function(t, b, c, v, detail, sx, sy, cx, cy, ctrl, alt, shift, meta, button, related) {
    this.initEvent(t, b, c);
    Object.assign(this, { view: v, detail: detail | 0, screenX: sx | 0, screenY: sy | 0, clientX: cx | 0, clientY: cy | 0, ctrlKey: !!ctrl, altKey: !!alt, shiftKey: !!shift, metaKey: !!meta, button: button | 0, buttons: 0, relatedTarget: related || null });
  } };
};
document.body = domNode("body");
document.head = domNode("head");
document.documentElement = domNode("html");
document.documentElement.appendChild(document.head);
document.documentElement.appendChild(document.body);
document.defaultCharset = "UTF-8";
document.hasFocus = function() {
  return false;
};
document.getElementsByTagName = function(tag) {
  var a = [];
  function visit(n) {
    if (tag === "*" || n.tagName === tag.toUpperCase()) a.push(n);
    for (var c of n.childNodes || []) visit(c);
  }
  visit(document.documentElement);
  return a;
};
eventTarget(document);
eventTarget(window);
window.parent = window;
window.top = window;
window.opener = null;
window.screen = { width: 0, height: 0, availWidth: 0, availHeight: 0, colorDepth: 24, pixelDepth: 24 };
window.devicePixelRatio = 1;
window.outerWidth = 1024;
window.outerHeight = 768;
window.innerWidth = 1024;
window.innerHeight = 768;
document = new Proxy(document, { get: function(t, k, r) {
  if (!(k in t)) print("document missing", String(k));
  return Reflect.get(t, k, r);
} });
