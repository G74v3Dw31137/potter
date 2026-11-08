// Public deterministic M1 observations. Run unchanged in Goja and pinned jsdom.
(function () {
  const out = {};
  const properties = ['src','currentSrc','width','height','naturalWidth','naturalHeight',
    'complete','alt','srcset','sizes','title','border','hspace','vspace','isMap','useMap','align','name','longDesc'];
  function value(v) {
    if (v === undefined) return {type:'undefined'};
    if (typeof v === 'bigint') return {bigint:String(v)};
    if (typeof v === 'symbol') return {symbol:v.description};
    if (v !== null && typeof v === 'object') return {type:'object'};
    if (typeof v === 'number' && (!Number.isFinite(v) || Object.is(v,-0))) return {number:String(v),negativeZero:Object.is(v,-0)};
    return v;
  }
  function attempt(fn) {
    try { return {value:value(fn())}; }
    catch(e) { return {error:e.name,message:e.message}; }
  }
  function state(img) {
    return {attributes:Array.from(img.attributes, a => [a.name,a.value]),
      values:properties.map(p => [p,attempt(() => img[p])])};
  }
  function descriptor(p) {
    let owner = HTMLImageElement.prototype, depth=0;
    while(owner && !Object.prototype.hasOwnProperty.call(owner,p)) {owner=Object.getPrototypeOf(owner);depth++;}
    if (!owner) return null;
    const d=Object.getOwnPropertyDescriptor(owner,p);
    function meta(fn) { return fn ? {name:fn.name,length:fn.length,prototype:Object.hasOwn(fn,'prototype')} : null; }
    return {depth,owner:owner.constructor.name,enumerable:d.enumerable,configurable:d.configurable,
      writable:value(d.writable),get:meta(d.get),set:meta(d.set)};
  }
  out.initial=state(document.createElement('img'));
  for(const p of properties) {
    out['descriptor.'+p]=descriptor(p);
    const cases = [ ['undefined',()=>undefined],['null',()=>null],['symbol',()=>Symbol('x')],
      ['bigint',()=>7n],['nan',()=>NaN],['infinity',()=>Infinity],['negativeInfinity',()=>-Infinity],
      ['negative',()=>-1],['fraction',()=>1.75],['negativeZero',()=>-0],['wrap',()=>4294967297],
      ['relative',()=> 'a.png'],['surrogates',()=> 'a\ud800b\udc00c\ud83d\ude00'],
      ['primitive',(log)=>({[Symbol.toPrimitive](hint){log.push(hint);return 3.75;}})],
      ['fallback',(log)=>({valueOf(){log.push('valueOf');return {};},toString(){log.push('toString');return '12';}})],
      ['throw',(log)=>({[Symbol.toPrimitive](hint){log.push(hint);throw new RangeError('public conversion');}})],
      ['objectSymbol',(log)=>({[Symbol.toPrimitive](hint){log.push(hint);return Symbol('object');}})],
      ['objectBigInt',(log)=>({[Symbol.toPrimitive](hint){log.push(hint);return 7n;}})] ];
    for(const [label,make] of cases) {
      const img=document.createElement('img'), log=[];
      img.setAttribute('src','before.png'); img.setAttribute(p.toLowerCase(),'before');
      const v=make(log), result=attempt(()=>{img[p]=v;return img[p];});
      out[p+'.'+label]={result,log,state:state(img)};
    }
    const d=Object.getOwnPropertyDescriptor(HTMLImageElement.prototype,p) || {};
    {
      for(const [label,receiver] of [['plain',{}],['forged',Object.create(HTMLImageElement.prototype)],
        ['structural',{getAttribute(){return '12';},hasAttribute(){return true;},setAttribute(){},removeAttribute(){},ownerDocument:document}],
        ['forgedStructural',Object.defineProperties(Object.create(HTMLImageElement.prototype),Object.getOwnPropertyDescriptors({getAttribute(){return '12';},hasAttribute(){return true;},setAttribute(){},removeAttribute(){},ownerDocument:document}))]]) {
        out[p+'.receiver.'+label]={get:d.get?attempt(()=>d.get.call(receiver)):null,set:d.set?attempt(()=>d.set.call(receiver,'2')):null};
      }
      if(d.set) {
        const img=document.createElement('img');
        out[p+'.omitted']={result:attempt(()=>d.set.call(img)),state:state(img)};
        const log=[], forged=Object.create(HTMLImageElement.prototype);
        Object.defineProperty(forged,'setAttribute',{get(){log.push('read setAttribute');return function(n,v){log.push(['mutate',n,v]);};}});
        Object.defineProperty(forged,'removeAttribute',{get(){log.push('read removeAttribute');return function(n){log.push(['remove',n]);};}});
        const result=attempt(()=>d.set.call(forged,{toString(){log.push('toString');return '2';},valueOf(){log.push('valueOf');return 2;}}));
        out[p+'.mutationOrder']={result,log};
      } else {out[p+'.omitted']=null;out[p+'.mutationOrder']=null;}
    }
  }
  const img=document.createElement('img');
  for(const raw of ['', '0','-0','-1','+12tail','  12.7','2147483647','2147483648','4294967295','Infinity']) {
    for(const p of ['width','height','hspace','vspace']) {
      img.setAttribute(p,raw);out[p+'.attribute.'+raw]=attempt(()=>img[p]);
    }
  }
  img.src='a.png'; img.longDesc='a.png';
  out.urlBefore={src:img.src,currentSrc:img.currentSrc,longDesc:img.longDesc,complete:img.complete};
  const base=document.createElement('base');base.setAttribute('href','https://example.test/assets/');document.head.appendChild(base);
  out.urlAfter={src:img.src,currentSrc:img.currentSrc,longDesc:img.longDesc,complete:img.complete};base.remove();
  img.setAttribute('src','');out.completeEmpty=img.complete;img.removeAttribute('src');out.completeAbsent=img.complete;
  return JSON.stringify(out);
})();
