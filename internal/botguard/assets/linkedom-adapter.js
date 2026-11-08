// Comparative DOM experiment. This is not a full browser implementation.
var baseCreateElement = document.createElement;
var baseCreateEvent = document.createEvent;
var linkedWindow = LD.parseHTML('<!DOCTYPE html><html lang="en"><head><title></title></head><body></body></html>');
for (var key of Object.keys(LD)) {
  if (/^(HTML|SVG)/.test(key) || ['Node','Element','Event','EventTarget','Document','DOMParser','CustomEvent','Text','Comment','DocumentFragment'].includes(key)) globalThis[key] = LD[key];
}
var linkedDocument = linkedWindow.document;
Object.assign(linkedDocument, {location:location, URL:location.href, documentURI:location.href, referrer:location.href, compatMode:'CSS1Compat', contentType:'text/html',characterSet:'UTF-8',readyState:'complete',hidden:true,visibilityState:'prerender',cookie:''});
linkedDocument.hasFocus = function(){return false};
var linkedCreate = linkedDocument.createElement.bind(linkedDocument);
linkedDocument.createElement = function(tag) {
  print('linked createElement',tag);
  var el = linkedCreate(tag);
  if (tag.toLowerCase() === 'iframe') el.contentWindow = baseCreateElement(tag).contentWindow;
  return el;
};
// LinkeDOM does not implement MouseEvent. Keep the explicitly limited adapter.
linkedDocument.createEvent = baseCreateEvent;
document = linkedDocument;
class UIEvent extends LD.Event {
  constructor(type, options={}) {
    super(type,options);
    this._ui={view:options.view||null,detail:(+options.detail)||0};
    const which=options.which;
    this._ui.which=which===undefined?0:(+which)>>>0;
  }
  get view(){if(!(this instanceof UIEvent))throw new TypeError('Illegal invocation');return this._ui.view;}
  get detail(){if(!(this instanceof UIEvent))throw new TypeError('Illegal invocation');return this._ui.detail;}
  // Legacy UIEventInit.which is an unsigned long. Synthetic mouse events keep
  // this initialized value; initUIEvent/initMouseEvent do not derive a key code.
  get which(){if(!(this instanceof UIEvent))throw new TypeError('Illegal invocation');return this._ui.which;}
  initEvent(type,bubbles=false,cancelable=false){
   if(!arguments.length)throw new TypeError('Missing type');
   if(!(this instanceof LD.Event))throw new TypeError('Illegal invocation');
   if(this.eventPhase!==0)return;
   if(typeof type==='symbol')throw new TypeError('Symbol');
   this.type=String(type);this.bubbles=!!bubbles;this.cancelable=!!cancelable;
   this.defaultPrevented=false;this.cancelBubble=false;this._stopImmediatePropagationFlag=false;
  }
  initUIEvent(type,bubbles=false,cancelable=false,view=null,detail=0){
   if(!arguments.length)throw new TypeError('Missing type');
   if(!(this instanceof UIEvent))throw new TypeError('Illegal invocation');
   if(this.eventPhase!==0)return;
   this.initEvent(type,bubbles,cancelable);this._ui={view,detail:(+detail)|0,which:this._ui.which};
  }
}
globalThis.UIEvent=UIEvent;
class MouseEvent extends UIEvent {
  constructor(type, options = {}) {
    super(type, options);
    this._mouse={};
    for(const key of ['screenX','screenY','clientX','clientY','button','buttons'])this._mouse[key]=(+options[key])|0;
    for(const key of ['ctrlKey','altKey','shiftKey','metaKey'])this._mouse[key]=!!options[key];
    this._mouse.relatedTarget=options.relatedTarget||null;
  }
  get pageX(){if(!(this instanceof MouseEvent))throw new TypeError('Illegal invocation');return this.clientX;}
  get pageY(){if(!(this instanceof MouseEvent))throw new TypeError('Illegal invocation');return this.clientY;}
  initMouseEvent(t,b,c,v,detail,sx,sy,cx,cy,ctrl,alt,shift,meta,button,related){if(this.eventPhase!==0)return;this.initUIEvent(t,b,c,v,detail);Object.assign(this._mouse,{screenX:sx|0,screenY:sy|0,clientX:cx|0,clientY:cy|0,ctrlKey:!!ctrl,altKey:!!alt,shiftKey:!!shift,metaKey:!!meta,button:button|0,relatedTarget:related||null})}
  get [Symbol.toStringTag](){return 'MouseEvent'}
}
for(const key of ['screenX','screenY','clientX','clientY','ctrlKey','altKey','shiftKey','metaKey','button','buttons','relatedTarget'])
 Object.defineProperty(MouseEvent.prototype,key,{enumerable:true,configurable:true,get(){if(!(this instanceof MouseEvent))throw new TypeError('Illegal invocation');return this._mouse[key];}});
globalThis.MouseEvent=MouseEvent;
for(const proto of [UIEvent.prototype,MouseEvent.prototype])for(const key of Object.getOwnPropertyNames(proto)){
 if(key==='constructor')continue;
 const d=Object.getOwnPropertyDescriptor(proto,key);d.enumerable=true;Object.defineProperty(proto,key,d);
}
var linkedCreateEvent=LD.Document.prototype.createEvent;
document.createEvent=function(name){print('linked createEvent',name);if(name==='MouseEvents'||name==='MouseEvent')return new MouseEvent('');if(name==='UIEvent'||name==='UIEvents')return new UIEvent('');return linkedCreateEvent.call(this,name)};

Object.defineProperty(document,"defaultView",{get:function(){return globalThis},configurable:true});

// DOM dispatch rejects legacy events that have not been initialized, and events
// already being dispatched. Preserve LinkeDOM's listener storage and traversal.
(function(){
 const dispatch=LD.EventTarget.prototype.dispatchEvent;
 LD.EventTarget.prototype.dispatchEvent=function(event){
  if(!(event instanceof LD.Event))throw new TypeError('Event required');
  if(!event.type||event.eventPhase!==0){const e=new Error('InvalidStateError');e.name='InvalidStateError';throw e;}
  return dispatch.call(this,event);
 };
})();
