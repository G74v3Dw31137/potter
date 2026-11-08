// Node.lookupPrefix is absent in LinkeDOM. Resolve using the namespace metadata
// carried by the node/ancestors; blank HTML trees have no prefix and return null.
// This does not add a full XML namespace parser to LinkeDOM.
// Unlike getAttributeNode(qualifiedName), this lookup is case-sensitive and
// matches both the namespace and local name. Return the existing Attr object.
Object.defineProperty(LD.Element.prototype, 'getAttributeNodeNS', {
 enumerable:true, configurable:true, writable:true,
 value:function getAttributeNodeNS(namespace, localName) {
  if (!(this instanceof LD.Element) || arguments.length < 2)
   throw new TypeError('getAttributeNodeNS requires an Element and two arguments');
  if (typeof namespace === 'symbol') throw new TypeError('Symbol');
  namespace = namespace == null ? null : String(namespace);
  if (namespace === '') namespace = null;
  if (typeof localName === 'symbol') throw new TypeError('Symbol');
  localName = String(localName);
  for (const attr of this.attributes)
   if ((attr.namespaceURI || null) === namespace && attr.localName === localName) return attr;
  return null;
 }
});

(function () {
  if ('lookupPrefix' in LD.Node.prototype) return;
  Object.defineProperty(LD.Node.prototype, 'lookupPrefix', {
    configurable: true, enumerable: true, writable: true,
    value: function lookupPrefix(namespace) {
      if (!(this instanceof LD.Node) || arguments.length === 0)
        throw new TypeError('lookupPrefix requires a Node and one argument');
      if (namespace == null || String(namespace) === '') return null;
      namespace = String(namespace);
      let node = this;
      if (node.nodeType === 9) node = node.documentElement;
      else if (node.nodeType === 2) node = node.ownerElement;
      else if (node.nodeType !== 1) node = node.parentElement;
      for (; node; node = node.parentElement) {
        if (node.namespaceURI === namespace && node.prefix) return node.prefix;
        for (const attr of Array.from(node.attributes || []))
          if (attr.prefix === 'xmlns' && attr.value === namespace) return attr.localName;
      }
      return null;
    }
  });
})();

Object.defineProperty(LD.Node.prototype,'isDefaultNamespace',{enumerable:true,configurable:true,writable:true,
 value:function isDefaultNamespace(namespace){
  if(!(this instanceof LD.Node)||!arguments.length)throw new TypeError('Illegal invocation');
  if(typeof namespace==='symbol')throw new TypeError('Symbol');
  namespace=namespace==null?'':String(namespace);
  let node=this, found=null;
  if(node.nodeType===9)node=node.documentElement;
  else if(node.nodeType===2)node=node.ownerElement;
  else if(node.nodeType!==1)node=node.nodeType===10||node.nodeType===11?null:node.parentElement;
  for(;node;node=node.parentElement){
   if(!node.prefix&&node.namespaceURI){found=node.namespaceURI;break;}
   if(node.hasAttribute('xmlns')){found=node.getAttribute('xmlns')||null;break;}
  }
  return (found||'')===namespace;
 }});

// DOM locate-a-namespace algorithm over LinkeDOM's available namespace metadata.
// LinkeDOM does not retain XMLNS attribute namespaces; declarations created with
// ordinary setAttribute are not namespace declarations for this algorithm.
Object.defineProperty(LD.Node.prototype, 'lookupNamespaceURI', {
 enumerable:true, configurable:true, writable:true,
 value:function lookupNamespaceURI(prefix) {
  if (!(this instanceof LD.Node) || !arguments.length) throw new TypeError('Illegal invocation');
  if (typeof prefix === 'symbol') throw new TypeError('Symbol');
  prefix = prefix == null ? null : String(prefix);
  if (prefix === '') prefix = null;
  let node = this;
  if (node.nodeType === 9) node = node.documentElement;
  else if (node.nodeType === 2) node = node.ownerElement;
  else if (node.nodeType === 10 || node.nodeType === 11) node = null;
  else if (node.nodeType !== 1) node = node.parentElement;
  for (; node; node = node.parentElement) {
   if (node.namespaceURI && (node.prefix || null) === prefix) return node.namespaceURI;
   for (const attr of Array.from(node.attributes || [])) {
    if (attr.namespaceURI !== 'http://www.w3.org/2000/xmlns/') continue;
    if (prefix === null ? (!attr.prefix && attr.localName === 'xmlns') :
        (attr.prefix === 'xmlns' && attr.localName === prefix)) return attr.value || null;
   }
  }
  return null;
 }
});
