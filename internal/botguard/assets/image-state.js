// M1: preserve image policies; shared helpers supply binding mechanisms.
(function (idl) {
  delete globalThis.pocWebIDL;
  // Legacy predicate intentionally reads LD.HTMLImageElement at call time.
  const checkImage = idl.receiver(image => image instanceof LD.HTMLImageElement, 'Illegal invocation');
  // LinkeDOM has no image fetching/source-selection pipeline. A freshly created,
  // unloaded HTMLImageElement has currentSrc == "" (read-only), like jsdom.
  // Setting src does not pretend that a network resource was loaded or selected.
  (function () {
    if ('currentSrc' in LD.HTMLImageElement.prototype) return;
    idl.accessor(LD.HTMLImageElement.prototype, 'currentSrc', {
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
    idl.accessors(LD.HTMLImageElement.prototype, {
      get complete() {
        checkImage(this);
        const src = this.getAttribute('src');
        return src === null || src === '';
      },
      get border() {
        checkImage(this);
        return this.getAttribute('border') || '';
      },
      set border(value) {
        checkImage(this);
        if (typeof value === 'symbol') throw new TypeError('Symbol');
        // Legacy null-to-empty is a border policy, not nullable DOMString.
        this.setAttribute('border', value === null ? '' : idl.domString(value));
      }
    });
  })();

  // Legacy image spacing reflects an unsigned integer content attribute; it is
  // independent of image loading and CSS layout. Match the limited unsigned-long
  // reflection used by HTMLImageElement (default/range fallback 0, maximum 2^31-1).
  (function () {
    for (const name of ['vspace','hspace']) {
   if (name in LD.HTMLImageElement.prototype) continue;
    const checkReceiver = idl.receiver(image => image instanceof LD.HTMLImageElement,
      'vspace requires an HTMLImageElement');
    idl.accessor(LD.HTMLImageElement.prototype, name, {
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
        const converted = idl.unsignedLong(value);
        this.setAttribute(name, String(converted <= 2147483647 ? converted : 0));
      }
    });
  }
  })();

  (function(){
   for(const [name,attr,boolean] of [['isMap','ismap',true],['useMap','usemap',false],['align','align',false],['name','name',false]]){
    idl.accessor(LD.HTMLImageElement.prototype,name,{enumerable:true,configurable:true,
     get(){checkImage(this);return boolean?this.hasAttribute(attr):(this.getAttribute(attr)||'');},
     set(value){checkImage(this);
      if(boolean){if(value)this.setAttribute(attr,'');else this.removeAttribute(attr);}
      else {
        // Keep direct-Symbol rejection BEFORE the observable setAttribute lookup.
        if(typeof value==='symbol')throw new TypeError('Symbol');
        this.setAttribute(attr,idl.domString(value));
      }
     }});
   }
  })();

  // longDesc is a URL-valued USVString reflection, not a plain DOMString.
  // Resolve on every read so base-element changes and adoption remain visible.
  (function(resolveURL) {
    delete globalThis.pocResolveDOMURL;
    const usvString = idl.usvString;
    idl.accessor(LD.HTMLImageElement.prototype, 'longDesc', {
      enumerable:true, configurable:true,
      get() {
        checkImage(this);
        const value = this.getAttribute('longdesc');
        if (value === null) return '';
        const doc = this.ownerDocument;
        let base = doc.URL || 'about:blank';
        const element = doc.querySelector('base[href]');
        if (element) base = resolveURL(usvString(element.getAttribute('href')), base);
        return resolveURL(usvString(value), base);
      },
      set(value) {
        checkImage(this);
        this.setAttribute('longdesc', usvString(value));
      }
    });
  })(pocResolveDOMURL);
})(pocWebIDL);
