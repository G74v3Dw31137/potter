package botguard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNodePositionConstants(t *testing.T) {
	m, _ := newFake(t)
	ps, err := m.staticPrograms(context.Background())
	require.NoError(t, err)
	rt := newRuntime(fixtureContext())
	err = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, err)
	value, err := rt.vm.RunString(`(function(){
 const names=['DISCONNECTED','PRECEDING','FOLLOWING','CONTAINS','CONTAINED_BY','IMPLEMENTATION_SPECIFIC'];
 const image=document.createElement('img');
 for(let i=0;i<names.length;i++){
  const name='DOCUMENT_POSITION_'+names[i], expected=1<<i;
  if(Node[name]!==expected||Node.prototype[name]!==expected||image[name]!==expected||document[name]!==expected)return false;
  for(const owner of [Node,Node.prototype]){
   const d=Object.getOwnPropertyDescriptor(owner,name);
   if(!d||d.writable||d.configurable||!d.enumerable)return false;
  }
 }
 const parent=document.createElement('div'), first=document.createElement('span'), second=document.createElement('span');
 document.body.appendChild(parent);parent.appendChild(first);parent.appendChild(second);
 return first.compareDocumentPosition(first)===0 &&
  first.compareDocumentPosition(second)===first.DOCUMENT_POSITION_FOLLOWING &&
  second.compareDocumentPosition(first)===second.DOCUMENT_POSITION_PRECEDING &&
  parent.compareDocumentPosition(first)===(Node.DOCUMENT_POSITION_FOLLOWING|Node.DOCUMENT_POSITION_CONTAINED_BY);
 })()`)
	require.NoError(t, err, "Node constants/descriptor/compareDocumentPosition mismatch")
	require.True(t, value.ToBoolean(), "Node constants/descriptor/compareDocumentPosition mismatch")
}

func TestVirtualWindowSurface(t *testing.T) {
	m, _ := newFake(t)
	ps, err := m.staticPrograms(context.Background())
	require.NoError(t, err)
	rt := newRuntime(fixtureContext())
	err = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, err)
	v, err := rt.vm.RunString(`(function(){
 if(window.screenY!==0||window.screenX!==0||screenTop!==screenY||screenLeft!==screenX||typeof getComputedStyle!=='function')return false;
 try{getComputedStyle({});return false}catch(e){if(e.name!=='TypeError')return false}
 try{getComputedStyle(document.createElement('div'));return false}catch(e){return e.name==='NotSupportedError'}
 })()`)
	require.NoError(t, err, "virtual window surface mismatch")
	require.True(t, v.ToBoolean(), "virtual window surface mismatch")
}
