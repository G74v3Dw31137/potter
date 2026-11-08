// LinkeDOM has no image fetching/source-selection pipeline. A freshly created,
// unloaded HTMLImageElement has currentSrc == "" (read-only), like jsdom.
// Setting src does not pretend that a network resource was loaded or selected.
(function () {
  if ('currentSrc' in LD.HTMLImageElement.prototype) return;
  Object.defineProperty(LD.HTMLImageElement.prototype, 'currentSrc', {
    configurable: true,
    enumerable: true,
    get() { return ''; }
  });
})();

// No image requests are started by this adapter. With no source (or an empty
// source) the image is complete; a nonempty source remains unavailable. There
// is no decoded/broken request state, srcset selection or synthetic load event.
// border is legacy DOMString reflection with null treated as the empty string.
(function () {
  Object.defineProperties(LD.HTMLImageElement.prototype, Object.getOwnPropertyDescriptors({
    get complete() {
      if (!(this instanceof LD.HTMLImageElement)) throw new TypeError('Illegal invocation');
      const src = this.getAttribute('src');
      return src === null || src === '';
    },
    get border() {
      if (!(this instanceof LD.HTMLImageElement)) throw new TypeError('Illegal invocation');
      return this.getAttribute('border') || '';
    },
    set border(value) {
      if (!(this instanceof LD.HTMLImageElement)) throw new TypeError('Illegal invocation');
      if (typeof value === 'symbol') throw new TypeError('Symbol');
      this.setAttribute('border', value === null ? '' : String(value));
    }
  }));
})();

// Legacy image spacing reflects an unsigned integer content attribute; it is
// independent of image loading and CSS layout. Match the limited unsigned-long
// reflection used by HTMLImageElement (default/range fallback 0, maximum 2^31-1).
(function () {
  for (const name of ['vspace','hspace']) {
 if (name in LD.HTMLImageElement.prototype) continue;
  function checkReceiver(image) {
    if (!(image instanceof LD.HTMLImageElement))
      throw new TypeError('vspace requires an HTMLImageElement');
  }
  Object.defineProperty(LD.HTMLImageElement.prototype, name, {
    configurable: true,
    enumerable: true,
    get() {
      checkReceiver(this);
      const raw = this.getAttribute(name);
      const match = raw === null ? null : /^[\t\n\f\r ]*([+-]?\d+)/.exec(raw);
      const value = match ? Number(match[1]) : NaN;
      return value >= 0 && value <= 2147483647 ? (value === 0 ? 0 : value) : 0;
    },
    set(value) {
      checkReceiver(this);
      // Unary + follows ToNumber, including TypeError for BigInt/Symbol.
      // >>> 0 implements WebIDL unsigned-long truncation/modulo conversion.
      const converted = (+value) >>> 0;
      this.setAttribute(name, String(converted <= 2147483647 ? converted : 0));
    }
  });
}
})();

(function(){
 for(const [name,attr,boolean] of [['isMap','ismap',true],['useMap','usemap',false],['align','align',false],['name','name',false]]){
  Object.defineProperty(LD.HTMLImageElement.prototype,name,{enumerable:true,configurable:true,
   get(){if(!(this instanceof LD.HTMLImageElement))throw new TypeError('Illegal invocation');return boolean?this.hasAttribute(attr):(this.getAttribute(attr)||'');},
   set(value){if(!(this instanceof LD.HTMLImageElement))throw new TypeError('Illegal invocation');
    if(boolean){if(value)this.setAttribute(attr,'');else this.removeAttribute(attr);}
    else {if(typeof value==='symbol')throw new TypeError('Symbol');this.setAttribute(attr,String(value));}
   }});
 }
})();

// longDesc is a URL-valued USVString reflection, not a plain DOMString.
// Resolve on every read so base-element changes and adoption remain visible.
(function(resolveURL) {
  delete globalThis.pocResolveDOMURL;
  function usvString(value) {
    if (typeof value === 'symbol') throw new TypeError('Symbol');
    const text = String(value);
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
  Object.defineProperty(LD.HTMLImageElement.prototype, 'longDesc', {
    enumerable:true, configurable:true,
    get() {
      if (!(this instanceof LD.HTMLImageElement)) throw new TypeError('Illegal invocation');
      const value = this.getAttribute('longdesc');
      if (value === null) return '';
      const doc = this.ownerDocument;
      let base = doc.URL || 'about:blank';
      const element = doc.querySelector('base[href]');
      if (element) base = resolveURL(usvString(element.getAttribute('href')), base);
      return resolveURL(usvString(value), base);
    },
    set(value) {
      if (!(this instanceof LD.HTMLImageElement)) throw new TypeError('Illegal invocation');
      this.setAttribute('longdesc', usvString(value));
    }
  });
})(pocResolveDOMURL);
