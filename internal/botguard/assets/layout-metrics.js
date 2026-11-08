// LinkeDOM does not render CSS boxes. Match no-layout DOM implementations:
// HTMLElement offsets are zero and there is no offset parent, including on
// attached/styled elements. This deliberately does NOT simulate CSS layout.
(function () {
  for (const name of ['offsetTop','offsetLeft','offsetWidth','offsetHeight']) {
    if (!(name in LD.HTMLElement.prototype))
      Object.defineProperty(LD.HTMLElement.prototype,name,{configurable:true,enumerable:true,get(){if (!(this instanceof LD.Element)) throw new TypeError('Illegal invocation'); return 0}});
  }
  if (!('offsetParent' in LD.HTMLElement.prototype))
    Object.defineProperty(LD.HTMLElement.prototype,'offsetParent',{configurable:true,enumerable:true,get(){return null}});
})();

// Observed BotGuard probe: Element.clientHeight must be numeric even when no
// CSS layout boxes exist. Do not invent a height from style/attribute values.
if (!('clientHeight' in LD.Element.prototype))
  Object.defineProperty(LD.Element.prototype,'clientHeight',{configurable:true,enumerable:true,get(){if (!(this instanceof LD.Element)) throw new TypeError('Illegal invocation'); return 0}});

// Like clientHeight, scrollWidth is zero without CSS layout, including for
// attached elements. Attribute/style widths do not represent rendered boxes.
if (!('scrollWidth' in LD.Element.prototype))
  Object.defineProperty(LD.Element.prototype,'scrollWidth',{configurable:true,enumerable:true,get(){if (!(this instanceof LD.Element)) throw new TypeError('Illegal invocation'); return 0}});

for (const name of ['clientWidth','scrollHeight','clientTop','clientLeft']) {
 if (!(name in LD.Element.prototype)) Object.defineProperty(LD.Element.prototype,name,{enumerable:true,configurable:true,get(){if (!(this instanceof LD.Element)) throw new TypeError('Illegal invocation'); return 0;}});
}
