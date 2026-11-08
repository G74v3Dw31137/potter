package botguard

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBrowserCompatibilityFamilies(t *testing.T) {
	m, _ := newFake(t)
	ps, err := m.staticPrograms(context.Background())
	require.NoError(t, err)
	rt := newRuntime(fixtureContext())
	err = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, err)
	_, err = rt.vm.RunString(`(function(){
 const assert=(v)=>{if(!v)throw Error('conformance assertion');};
 const throws=f=>{let ok=false;try{f()}catch(e){ok=e instanceof TypeError}assert(ok)};
 const img=document.createElement('img');
 for(const key of ['scrollTop','scrollLeft']){
 const d=Object.getOwnPropertyDescriptor(Element.prototype,key);
 assert(d.enumerable&&d.configurable&&d.get&&d.set&&img[key]===0);
 img[key]='12.5';assert(img[key]===12.5);throws(()=>d.get.call({}));throws(()=>{img[key]=1n});
 }
 for(const key of ['clientWidth','clientHeight','scrollWidth','scrollHeight']){
 const d=Object.getOwnPropertyDescriptor(Element.prototype,key);assert(d.get&&!d.set&&img[key]===0);throws(()=>d.get.call({}));
 }
 for(const key of ['hspace','vspace']){
 const d=Object.getOwnPropertyDescriptor(HTMLImageElement.prototype,key);assert(d.get&&d.set&&d.enumerable&&d.configurable);
 img.setAttribute(key,' +12px');assert(img[key]===12);img[key]=4294967297;assert(img[key]===1);
 throws(()=>d.set.call({},2));throws(()=>{img[key]=Symbol()});throws(()=>{img[key]=1n});
 assert(img.cloneNode()[key]===1);img.removeAttribute(key);assert(img[key]===0);
 }
 img.isMap='false';assert(img.isMap&&img.getAttribute('ismap')==='');
 img.isMap=0;assert(!img.hasAttribute('ismap'));img.setAttribute('ismap','false');assert(img.isMap);
 img.useMap=null;assert(img.useMap==='null');assert(img.cloneNode().useMap==='null');img.removeAttribute('usemap');assert(img.useMap==='');
 for(const p of [Node,Node.prototype]){const d=Object.getOwnPropertyDescriptor(p,'NOTATION_NODE');assert(d.value===12&&d.enumerable&&!d.writable&&!d.configurable)}
 assert(document.isDefaultNamespace('http://www.w3.org/1999/xhtml'));
 assert(!document.isDefaultNamespace(null));assert(document.createDocumentFragment().isDefaultNamespace(''));
 throws(()=>Node.prototype.isDefaultNamespace.call({},null));throws(()=>img.isDefaultNamespace());throws(()=>img.isDefaultNamespace(Symbol()));
 const e=document.createEvent('MouseEvents');assert(e instanceof MouseEvent&&e instanceof UIEvent&&e instanceof Event);
 e.initMouseEvent('click',true,true,null,2,3,4,5,6,false,false,false,false,0,null);
 assert(e.pageY===6&&e.pageX===5&&e.detail===2);
 const d=Object.getOwnPropertyDescriptor(MouseEvent.prototype,'pageY');assert(d.get&&!d.set&&d.enumerable&&d.configurable);throws(()=>d.get.call({}));
 let calls=0;img.addEventListener('click',ev=>{calls++;ev.preventDefault()});
 assert(img.dispatchEvent(e)===false&&calls===1&&e.eventPhase===0);
 e.initUIEvent('other',false,false,null,7);assert(e.type==='other'&&e.detail===7&&!e.defaultPrevented);
 })()`)
	require.NoError(t, err)
}

// Public probe preserves observations against the pinned jsdom reference without
// private challenge data. Window assignment conversion has separate coverage:
// jsdom implements name as an ordinary data property rather than a DOMString.
func TestRemainingDOMSurface(t *testing.T) {
	m, _ := newFake(t)
	ps, err := m.staticPrograms(context.Background())
	require.NoError(t, err)
	rt := newRuntime(fixtureContext())
	err = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, err)
	probe, err := os.ReadFile("testdata/dom-surface-probe.js")
	require.NoError(t, err)
	_, err = rt.vm.RunString(string(probe))
	require.NoError(t, err)
	_, err = rt.vm.RunString(`
 for(const value of [null,undefined,12n,'frame']){
  globalThis.name=value;if(globalThis.name!==String(value))throw Error('Window.name conversion');
 }
 let threw=false;try{globalThis.name=Symbol()}catch(e){threw=e instanceof TypeError}
 if(!threw)throw Error('Window.name Symbol');
 const descriptor=Object.getOwnPropertyDescriptor(globalThis,'name');
 if(!descriptor.enumerable||!descriptor.configurable||!descriptor.get||!descriptor.set)throw Error('Window.name descriptor');
 `)
	require.NoError(t, err)
}

func TestNodeAttributeImageURLSurface(t *testing.T) {
	m, _ := newFake(t)
	ps, err := m.staticPrograms(context.Background())
	require.NoError(t, err)
	rt := newRuntime(fixtureContext())
	err = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, err)
	probe, err := os.ReadFile("testdata/node-attribute-image-url-probe.js")
	require.NoError(t, err)
	_, err = rt.vm.RunString(string(probe))
	require.NoError(t, err)
	// jsdom 29.1.1 has a URL-getter cache defect for base-only changes. Keep the
	// live-base behavior checked here independently of the shared reference probe.
	_, err = rt.vm.RunString(`(function(){
 const img=document.createElement('img'),base=document.createElement('base');
 base.setAttribute('href','/one/');document.head.appendChild(base);img.longDesc='same';
 if(img.longDesc!=='https://m.youtube.com/one/same')throw Error('initial base');
 base.setAttribute('href','/two/');
 if(img.longDesc!=='https://m.youtube.com/two/same')throw Error('live base');
 base.remove();
 if(img.longDesc!=='https://m.youtube.com/same')throw Error('removed base');
 })()`)
	require.NoError(t, err)
}
