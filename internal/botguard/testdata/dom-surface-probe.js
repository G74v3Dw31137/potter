(function(w) {
 const assert=(v,label)=>{if(!v)throw Error(label)};
 const throws=(f,label)=>{let ok=false;try{f()}catch(e){ok=e instanceof TypeError}assert(ok,label)};
 const img=w.document.createElement('img');
 for(const [key,value] of [['ENTITY_REFERENCE_NODE',5],['ENTITY_NODE',6],['NOTATION_NODE',12]]) {
  for(const target of [w.Node,w.Node.prototype]) {
   const d=Object.getOwnPropertyDescriptor(target,key);
   assert(d.value===value&&d.enumerable&&!d.configurable&&!d.writable,key+' descriptor');
  }
  assert(img[key]===value,key+' instance');
 }
 for(const key of ['align','name']) {
  const d=Object.getOwnPropertyDescriptor(w.HTMLImageElement.prototype,key);
  assert(d.get&&d.set&&d.enumerable&&d.configurable,key+' descriptor');
  assert(img[key]==='',key+' default');
  img[key]=null;assert(img[key]==='null'&&img.getAttribute(key)==='null',key+' null');
  img[key]=42n;assert(img[key]==='42',key+' bigint');
  img.setAttribute(key,'left');assert(img[key]==='left'&&img.cloneNode()[key]==='left',key+' attribute/clone');
  img.removeAttribute(key);assert(img[key]==='',key+' remove');
  throws(()=>d.get.call({}),key+' receiver');throws(()=>d.set.call({},'x'),key+' setter receiver');
  throws(()=>{img[key]=Symbol()},key+' symbol');
 }
 const ns='http://www.w3.org/1999/xhtml';
 const lookup=w.Node.prototype.lookupNamespaceURI;
 const d=Object.getOwnPropertyDescriptor(w.Node.prototype,'lookupNamespaceURI');
 assert(d.enumerable&&d.configurable&&d.writable,'lookup descriptor');
 for(const n of [img,w.document,w.document.documentElement]) {
  for(const p of [null,undefined,''])assert(n.lookupNamespaceURI(p)===ns,'default namespace');
  assert(n.lookupNamespaceURI('unknown')===null,'unknown prefix');
 }
 const text=w.document.createTextNode('');img.appendChild(text);
 assert(text.lookupNamespaceURI(null)===ns,'text parent');
 const attr=w.document.createAttribute('test');assert(attr.lookupNamespaceURI(null)===null,'detached attr');
 img.setAttributeNode(attr);assert(attr.lookupNamespaceURI(null)===ns,'owned attr');
 for(const n of [w.document.createDocumentFragment(),w.document.doctype,w.document.createTextNode('')])
  assert(n.lookupNamespaceURI(null)===null,'no namespace context');
 assert(w.document.createElementNS('http://www.w3.org/2000/svg','svg').lookupNamespaceURI(null)==='http://www.w3.org/2000/svg','SVG namespace');
 throws(()=>lookup.call({},null),'namespace receiver');throws(()=>lookup.call(img),'namespace argument');
 throws(()=>img.lookupNamespaceURI(Symbol()),'namespace symbol');
 assert(w.name==='','window name default');
})(globalThis);
