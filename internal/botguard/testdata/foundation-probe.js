(function(){
 const out={surface:{},behavior:{}};
 function val(x){if(x===undefined)return {type:'undefined'};if(typeof x==='number'&&!Number.isFinite(x))return String(x);if(x===null||['number','boolean','string'].includes(typeof x))return x;return {type:typeof x,tag:Object.prototype.toString.call(x)}}
 function probe(k,fn){try{out.behavior[k]=val(fn())}catch(e){out.behavior[k]={error:e.name,message:e.message}}}
 function desc(o,k){let depth=0;for(let p=o;p;p=Object.getPrototypeOf(p),depth++){let d=Object.getOwnPropertyDescriptor(p,k);if(d)return {depth,owner:p===o?'instance':(p.constructor&&p.constructor.name),enumerable:d.enumerable,configurable:d.configurable,...('value'in d?{kind:'data',writable:d.writable,type:typeof d.value}:{kind:'accessor',get:typeof d.get,set:typeof d.set})}}return null}
 const img=document.createElement('img'),div=document.createElement('div'),ev=document.createEvent('MouseEvents');
 const sets={document:[document,'URL defaultView createElement createEvent'],image:[img,'src width naturalWidth complete currentSrc border hspace vspace longDesc'],element:[div,'childNodes children attributes getAttributeNodeNS setAttributeNS getBoundingClientRect clientWidth'],event:[ev,'type target currentTarget eventPhase cancelable defaultPrevented timeStamp detail clientX button initUIEvent initMouseEvent'],navigator:[navigator,'userAgent'],screen:[screen,'width'],location:[location,'href']};
 for(const [name,[o,keys]]of Object.entries(sets)){out.surface[name]={};for(const k of keys.split(' '))out.surface[name][k]=desc(o,k)}
 probe('window.tag',()=>Object.prototype.toString.call(globalThis));
 probe('document.windowIdentity',()=>document.defaultView===globalThis);
 probe('collections.array',()=>Array.isArray(div.childNodes));
 probe('collections.childNodes.sameObject',()=>div.childNodes===div.childNodes);
 probe('collections.childNodes.live',()=>{const p=document.createElement('div'),a=p.childNodes;p.appendChild(document.createElement('b'));return a.length});
 probe('collections.children.live',()=>{const p=document.createElement('div'),a=p.children;p.appendChild(document.createElement('b'));return a.length});
 probe('collections.attributes.live',()=>{const p=document.createElement('div'),a=p.attributes;p.setAttribute('x','1');return a.length});
 probe('collections.query.static',()=>{const p=document.createElement('div'),a=p.querySelectorAll('b');p.appendChild(document.createElement('b'));return a.length});
 probe('collections.nodelist.push',()=>typeof div.childNodes.push);
 probe('collections.named',()=>{const p=document.createElement('div');p.setAttribute('id','x');return p.attributes.id===p.getAttributeNode('id')});
 probe('attribute.htmlCase',()=>{const p=document.createElement('div');p.setAttribute('DATA-X','1');return p.getAttribute('data-x')});
 probe('attribute.namespace',()=>{const p=document.createElement('div');p.setAttributeNS('urn:a','p:x','1');return p.getAttributeNodeNS('urn:a','x')!==null});
 probe('attribute.lookupPrefix.symbol',()=>div.lookupPrefix(Symbol('x')));
 probe('attribute.lookupPrefix.coercions',()=>{let n=0;div.lookupPrefix({toString(){n++;return 'urn:x'}});return n});
 probe('receiver.currentSrc',()=>Object.getOwnPropertyDescriptor(HTMLImageElement.prototype,'currentSrc').get.call({}));
 probe('receiver.clientWidth.forged',()=>Object.getOwnPropertyDescriptor(Element.prototype,'clientWidth').get.call(Object.create(Element.prototype)));
 probe('receiver.offsetParent',()=>Object.getOwnPropertyDescriptor(HTMLElement.prototype,'offsetParent').get.call({}));
 probe('constructor.UIEvent.noargs',()=>new UIEvent().type);
 probe('constructor.Event.noargs',()=>new Event().type);
 probe('constructor.UIEvent.symbol',()=>new UIEvent(Symbol()).type);
 probe('constructor.UIEvent.detail',()=>new UIEvent('x',{detail:1.75}).detail);
 probe('constructor.MouseEvent.fraction',()=>new MouseEvent('x',{clientX:1.75}).clientX);
 probe('constructor.MouseEvent.button',()=>new MouseEvent('x',{button:65535}).button);
 probe('constructor.UIEvent.nullDictionary',()=>new UIEvent('x',null).detail);
 probe('constructor.UIEvent.view',()=>new UIEvent('x',{view:{}}).view===null);
 probe('constructor.EventTarget',()=>Object.prototype.toString.call(new EventTarget()));
 probe('event.type.readonly',()=>{const e=new Event('a');e.type='b';return e.type});
 probe('event.preventDefault.uncancelable',()=>{const e=new Event('a');e.preventDefault();return e.defaultPrevented});
 probe('event.captureOrder',()=>{const p=document.createElement('div'),c=document.createElement('b');p.appendChild(c);let a=[];p.addEventListener('x',()=>a.push('capture'),true);p.addEventListener('x',()=>a.push('bubble'));c.addEventListener('x',()=>a.push('target'));c.dispatchEvent(new Event('x',{bubbles:true}));return a.join(',')});
 probe('window.eventDuplicate',()=>{let n=0;const f=()=>n++;window.addEventListener('audit',f);window.addEventListener('audit',f);window.dispatchEvent(new Event('audit'));window.removeEventListener('audit',f);return n});
 probe('event.dispatch.exception',()=>div.dispatchEvent(document.createEvent('Event')));
 probe('image.width.negative',()=>{const x=document.createElement('img');x.width=-1;return x.width});
 probe('image.src.relative',()=>{const x=document.createElement('img');x.src='a.png';return x.src});
 probe('image.complete.srcset',()=>{const x=document.createElement('img');x.srcset='a.png 1x';return x.complete});
 probe('image.border.null',()=>{const x=document.createElement('img');x.border=null;return x.border});
 probe('style.computed',()=>getComputedStyle(div).display);
 probe('geometry.rect.tag',()=>Object.prototype.toString.call(div.getBoundingClientRect()));
 probe('geometry.rects',()=>div.getClientRects().length);
 probe('encoding.atob.error',()=>atob('%'));
 probe('encoding.btoa.error',()=>btoa('\u0100'));
 probe('timer.timeout.string',()=>{let h=setTimeout('void 0',10000);clearTimeout(h);return typeof h});
 probe('timer.interval.missing',()=>{let h=setInterval();clearInterval(h);return typeof h});
 probe('history.prototype',()=>Object.getPrototypeOf(history)===null);
 probe('iframe.realm',()=>{const f=document.createElement('iframe');document.body.appendChild(f);let same=f.contentWindow.Array===Array;f.remove();return same});
 probe('iframe.document',()=>{const f=document.createElement('iframe');document.body.appendChild(f);let t=typeof f.contentWindow.document;f.remove();return t});
 probe('event.failedInit.typeState',()=>{const e=new UIEvent('old',{detail:5});try{e.initUIEvent('new',true,true,null,Symbol())}catch{}return e.type});
 probe('constructor.UIEvent.objectBigInt',()=>new UIEvent('x',{detail:{valueOf(){return 1n}}}).detail);
 probe('constructor.MouseEvent.relatedTarget',()=>new MouseEvent('x',{relatedTarget:{}}).relatedTarget===null);
 probe('timer.timeout.arity',()=>setTimeout.length);
 probe('timer.interval.arity',()=>setInterval.length);
 probe('global.encoding.encodeSymbol',()=>typeof TextEncoder==='undefined'?'missing':Array.from(new TextEncoder().encode(Symbol('x'))).length);

 for(const C of [UIEvent,MouseEvent]) {
  const n=C.name;
  for(const [k,args] of [['missing',[]],['undefined',[undefined]],['null',[null]],['dictUndefined',['x',undefined]],['dictNull',['x',null]],['dictPrimitive',['x',1]]])probe('arguments.'+n+'.'+k,()=>new C(...args).type);
  for(const [k,value] of [['nan',NaN],['infinity',Infinity],['negative',-1.75],['wrap',4294967297],['bigint',1n],['symbol',Symbol()]])
   probe('numeric.'+n+'.'+k,()=>{const e=new C('x',{detail:value,clientX:value,button:value});return JSON.stringify([e.detail,e.clientX,e.button])});
 }
 probe('dictionary.sideEffects',()=>{let a=[];const e=new MouseEvent('x',{get detail(){a.push('detail');return {valueOf(){a.push('valueOf');return 2}}},get clientX(){a.push('clientX');return 3}});return a.join(',')});
 probe('initializer.atomicity',()=>{const e=new MouseEvent('old',{detail:5,clientX:7});let error;try{e.initMouseEvent('new',true,true,null,2,3,4,Symbol(),6,false,false,false,false,0,null)}catch(x){error=x.name}return JSON.stringify([error,e.type,e.bubbles,e.cancelable,e.detail,e.clientX])});
 probe('event.dispatchState',()=>{const p=document.createElement('div'),e=new Event('x',{cancelable:true});let a=[e.eventPhase,e.target===null,e.currentTarget===null];p.addEventListener('x',()=>{a.push(e.eventPhase,e.target===p,e.currentTarget===p);e.preventDefault();try{p.dispatchEvent(e)}catch(x){a.push(x.name)}});a.push(p.dispatchEvent(e),e.eventPhase,e.target===p,e.currentTarget===null);return JSON.stringify(a)});
 probe('event.duplicateNode',()=>{const p=document.createElement('div');let n=0;function f(){n++}p.addEventListener('x',f);p.addEventListener('x',f);p.dispatchEvent(new Event('x'));return n});
 for(const key of ['type','target','eventPhase','timeStamp','defaultPrevented'])probe('event.readonly.'+key,()=>{const e=new Event('old');const before=e[key];try{e[key]='changed'}catch{}return e[key]===before});
 for(const key of ['childNodes','children','attributes']){
  probe('collection.'+key+'.identity',()=>div[key]===div[key]);
  probe('collection.'+key+'.array',()=>Array.isArray(div[key]));
  probe('collection.'+key+'.prototype',()=>Object.getPrototypeOf(Object.getPrototypeOf(div[key]))===Object.prototype);
  probe('collection.'+key+'.splice',()=>typeof div[key].splice);
  out.surface['collection.'+key]={length:desc(div[key],'length'),push:desc(div[key],'push'),item:desc(div[key],'item')};
 }
 for(const [proto,key] of [[HTMLImageElement.prototype,'complete'],[HTMLImageElement.prototype,'border'],[Element.prototype,'clientWidth'],[Node.prototype,'lookupPrefix'],[Element.prototype,'getAttribute']]){
  const d=Object.getOwnPropertyDescriptor(proto,key),f=d.get||d.value;
  for(const [kind,receiver] of [['plain',{}],['forged',Object.create(proto)]])probe('brand.'+proto.constructor.name+'.'+key+'.'+kind,()=>f.call(receiver,'x'));
 }
 probe('namespace.setAttribute.symbol',()=>div.setAttribute(Symbol(),'v'));
 probe('namespace.lookupPrefix.primitive',()=>{let a=[];div.lookupPrefix({[Symbol.toPrimitive](hint){a.push(hint);return 'urn:x'}});return a.join(',')});
 return JSON.stringify(out);
})()
