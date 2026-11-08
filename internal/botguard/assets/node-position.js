// DOM Node constants must be visible on both the constructor and instances.
// LinkeDOM uses these values internally in compareDocumentPosition, but does not
// expose them. https://dom.spec.whatwg.org/#interface-node
(function () {
  const constants = {
    PROCESSING_INSTRUCTION_NODE: 7,
    ENTITY_REFERENCE_NODE: 5,
    ENTITY_NODE: 6,
    NOTATION_NODE: 12,
    DOCUMENT_POSITION_DISCONNECTED: 0x01,
    DOCUMENT_POSITION_PRECEDING: 0x02,
    DOCUMENT_POSITION_FOLLOWING: 0x04,
    DOCUMENT_POSITION_CONTAINS: 0x08,
    DOCUMENT_POSITION_CONTAINED_BY: 0x10,
    DOCUMENT_POSITION_IMPLEMENTATION_SPECIFIC: 0x20
  };
  for (const target of [LD.Node, LD.Node.prototype]) {
    for (const [name, value] of Object.entries(constants)) {
      if (!(name in target))
        Object.defineProperty(target, name, {value, enumerable: true});
    }
  }
})();
