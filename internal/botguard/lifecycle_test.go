package botguard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWindowFrameCount(t *testing.T) {
	m, _ := newFake(t)
	ps, err := m.staticPrograms(context.Background())
	require.NoError(t, err)
	rt := newRuntime(fixtureContext())
	err = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, err)
	v, err := rt.vm.RunString(`(function(){
  if(window.length!==0||frames!==window)return false;
  const a=document.createElement('iframe'),b=document.createElement('iframe');
  if(window.length!==0)return false;
  document.body.appendChild(a);document.body.appendChild(b);
  if(window.length!==2)return false;
  a.remove();if(window.length!==1)return false;
  b.remove();return window.length===0;
 })()`)
	require.NoError(t, err, "frame count is not live")
	require.True(t, v.ToBoolean(), "frame count is not live")
}
func TestNoLayoutMetrics(t *testing.T) {
	m, _ := newFake(t)
	ps, err := m.staticPrograms(context.Background())
	require.NoError(t, err)
	rt := newRuntime(fixtureContext())
	err = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, err)
	v, err := rt.vm.RunString(`(function(){
 const image=document.createElement('img');
 if(image.clientHeight!==0||document.body.clientHeight!==0)return false;
 const scrollDescriptor=Object.getOwnPropertyDescriptor(Element.prototype,'scrollWidth');
 if(!scrollDescriptor||!scrollDescriptor.get||scrollDescriptor.set||!scrollDescriptor.enumerable||!scrollDescriptor.configurable)return false;
 const clientDescriptor=Object.getOwnPropertyDescriptor(Element.prototype,'clientHeight');
 if(!clientDescriptor||typeof clientDescriptor.get!=='function'||clientDescriptor.set)return false;
 image.style.cssText='position:absolute;top:30px;width:50px;height:20px';
 for(let attached of [false,true]){
  if(attached)document.body.appendChild(image);
  for(const name of ['offsetTop','offsetLeft','offsetWidth','offsetHeight']){
   if(image[name]!==0)return false;
   const d=Object.getOwnPropertyDescriptor(HTMLElement.prototype,name);
   if(!d||!d.get||d.set)return false;
  }
  if(image.offsetParent!==null||image.scrollWidth!==0||document.body.scrollWidth!==0)return false;
 }
 return true;
 })()`)
	require.NoError(t, err, "no-layout metric mismatch")
	require.True(t, v.ToBoolean(), "no-layout metric mismatch")
}

func TestInterruptedRuntimeDoesNotReenterVM(t *testing.T) {
	rt := newRuntime(fixtureContext())
	// A getter would execute JavaScript and encounter the pending interrupt if
	// any later operation tried to inspect the runtime after cancellation.
	require.NoError(t, rt.eval(`Object.setPrototypeOf(globalThis,new Proxy({}, {get(){throw Error('PRIVATE diagnostic getter')}}))`))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := rt.run(ctx, ErrSnapshot, func(context.Context) error { return nil })
	require.ErrorIs(t, err, context.Canceled, "cancellation was hidden")
	rt.vm.Interrupt("PRIVATE interruption")
	called := false
	require.ErrorIs(t, rt.run(context.Background(), ErrMint, func(context.Context) error { called = true; rt.vm.Get("unused"); return nil }), ErrMint)
	require.False(t, called)
	require.True(t, rt.interrupted)
	require.True(t, rt.broken)
}

func TestCollectionPrototypeAndVirtualHistory(t *testing.T) {
	m, _ := newFake(t)
	ps, e := m.staticPrograms(context.Background())
	require.NoError(t, e)
	rt := newRuntime(fixtureContext())
	e = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, e)
	v, e := rt.vm.RunString(`(function(){
  const parent=document.createElement('div');parent.innerHTML='<span></span><b></b>';
  const nodes=parent.childNodes;
  if(Object.getPrototypeOf(Object.getPrototypeOf(nodes))!==Object.prototype||nodes instanceof Array)return false;
  if(nodes.length!==2||nodes.item(0).localName!=='span'||Array.from(nodes).length!==2||nodes.map(n=>n.localName).join(',')!=='span,b')return false;
  const clone=parent.cloneNode(true);if(clone.childNodes.length!==2||clone.querySelectorAll('span,b').length!==2)return false;
  parent.firstChild.remove();if(parent.childNodes.length!==1)return false;
  if(typeof history!=='object'||history.length!==1||history.state!==null)return false;
  const d=Object.getOwnPropertyDescriptor(history,'length');return !!d.get&&!d.set;
 })()`)
	require.NoError(t, e, "collection/history contract mismatch")
	require.True(t, v.ToBoolean(), "collection/history contract mismatch")
}

func TestNamedNodeMapPrototypeAndOperations(t *testing.T) {
	m, _ := newFake(t)
	ps, e := m.staticPrograms(context.Background())
	require.NoError(t, e)
	rt := newRuntime(fixtureContext())
	e = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, e)
	v, e := rt.vm.RunString(`(function(){
  const el=document.createElement('img');el.setAttribute('lang','en');el.setAttribute('title','test');
  const attrs=el.attributes,p=Object.getPrototypeOf(attrs);
  if(Object.getPrototypeOf(p)!==Object.prototype||attrs instanceof Array)return false;
  if(attrs.length!==2||!attrs.item(0).name||attrs.getNamedItem('lang').value!=='en'||Array.from(attrs).length!==2)return false;
  const a=document.createAttribute('alt');a.value='image';attrs.setNamedItem(a);
  if(el.getAttribute('alt')!=='image'||!attrs.getNamedItem('alt'))return false;
  attrs.removeNamedItem('alt');if(el.hasAttribute('alt'))return false;
  const clone=el.cloneNode(true);return clone.attributes.length===2&&clone.attributes.getNamedItem('title').value==='test';
 })()`)
	require.NoError(t, e, "NamedNodeMap prototype or operations regression")
	require.True(t, v.ToBoolean(), "NamedNodeMap prototype or operations regression")
}

func TestImageVspaceReflection(t *testing.T) {
	m, _ := newFake(t)
	ps, err := m.staticPrograms(context.Background())
	require.NoError(t, err)
	rt := newRuntime(fixtureContext())
	err = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, err)
	v, err := rt.vm.RunString(`(function(){
 const img=document.createElement('img');
 const d=Object.getOwnPropertyDescriptor(HTMLImageElement.prototype,'vspace');
 if(!d||!d.get||!d.set||!d.enumerable||!d.configurable||img.vspace!==0||img.hasAttribute('vspace'))return false;
 for(const [raw,want] of [['',0],['12',12],['  +12px',12],['-1',0],['-0',0],['1.9',1],['0x10',0],['2147483647',2147483647],['2147483648',0],['4294967297',0],['\u00a012',0]]){
  img.setAttribute('vspace',raw);if(img.vspace!==want||Object.is(img.vspace,-0))return false;
 }
 for(const [value,want] of [[12.9,12],[-1,0],[2147483648,0],[4294967297,1],[NaN,0],[Infinity,0],[null,0],['7',7]]){
  img.vspace=value;if(img.vspace!==want||img.getAttribute('vspace')!==String(want))return false;
 }
 for(const value of [1n,Symbol('x')]){let error;try{img.vspace=value}catch(e){error=e}if(!(error instanceof TypeError)||img.vspace!==7)return false;}
 for(const operation of [()=>d.get.call({}),()=>d.set.call({},1)]){let error;try{operation()}catch(e){error=e}if(!(error instanceof TypeError))return false;}
 const clone=img.cloneNode();if(clone.vspace!==7)return false;
 img.removeAttribute('vspace');return img.vspace===0;
 })()`)
	require.NoError(t, err, "image spacing reflection mismatch")
	require.True(t, v.ToBoolean(), "image spacing reflection mismatch")
}
