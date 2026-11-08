(function (idl) {
  const results = {};
  function assert(ok) { if (!ok) throw new Error('assertion failed'); }
  function same(a,b) { assert(Object.is(a,b)); }
  function throws(fn, Class, message) {
    let error; try {fn();} catch(e) {error=e;}
    assert(error instanceof Class);
    if (message !== undefined) same(error.message,message);
    return error;
  }
  function test(name, fn) {
    try {fn();results[name]=true;} catch(e) {results[name]={error:e.name,message:e.message};}
  }
  for (const [label,input,expected] of [['undefined',undefined,'undefined'],['null',null,'null'],
    ['boolean',true,'true'],['number',-1.5,'-1.5'],['negativeZero',-0,'0'],['nan',NaN,'NaN'],
    ['infinity',Infinity,'Infinity'],['bigint',9n,'9'],['string','x','x']]) {
    test('DOMString.'+label,()=>same(idl.domString(input),expected));
  }
  test('DOMString.symbol',()=>throws(()=>idl.domString(Symbol()),TypeError,'Symbol'));
  test('DOMString.objectSymbol',()=>throws(()=>idl.domString({[Symbol.toPrimitive](){return Symbol();}}),TypeError));
  test('DOMString.objectBigInt',()=>same(idl.domString({[Symbol.toPrimitive](){return 9n;}}),'9'));
  test('DOMString.order',()=>{
    const log=[];
    same(idl.domString({toString(){log.push('toString');return {};},valueOf(){log.push('valueOf');return 2;}}),'2');
    same(log.join(','),'toString,valueOf');
  });
  test('DOMString.hintOnce',()=>{let log=[];same(idl.domString({[Symbol.toPrimitive](hint){log.push(hint);return 'x';}}),'x');same(log.join(','),'string');});
  test('DOMString.userException',()=>{const error={public:true};same(throws(()=>idl.domString({toString(){throw error;}}),Object),error);});
  test('USVString.surrogates',()=>same(idl.usvString('\ud800a\udc00\ud83d\ude00\ud800\ud800'),'\ufffda\ufffd\ud83d\ude00\ufffd\ufffd'));
  test('USVString.symbol',()=>throws(()=>idl.usvString(Symbol()),TypeError,'Symbol'));
  test('USVString.once',()=>{let n=0;same(idl.usvString({toString(){n++;return '\ud800';}}),'\ufffd');same(n,1);});
  test('nullable.nullish',()=>{const fail=()=>{throw Error('must not convert');};same(idl.nullable(null,fail),null);same(idl.nullable(undefined,fail),null);same(idl.nullable(3,idl.domString),'3');});
  test('arguments.presence',()=>{
    const args=(function(){return arguments;})(undefined,null,4);
    same(idl.argument(args,0).present,true);same(idl.argument(args,0).value,undefined);
    same(idl.argument(args,1).value,null);same(idl.argument(args,2).value,4);
    same(idl.argument(args,3).present,false);same(idl.argument(args,3).value,undefined);
    same(idl.required(args,0,'required'),undefined);throws(()=>idl.required(args,3,'required'),TypeError,'required');
  });
  test('arguments.defaults',()=>{
    const args=[undefined,null,4];same(idl.defaulted(idl.argument(args,3),9,false),9);
    same(idl.defaulted(idl.argument(args,0),9,false),undefined);
    same(idl.defaulted(idl.argument(args,0),9,true),9);
    same(idl.defaulted(idl.argument(args,1),9,true),null);same(idl.defaulted(idl.argument(args,2),9,true),4);
  });
  test('arguments.readOrder',()=>{
    const log=[], args={get length(){log.push('length');return 1;},get 0(){log.push('value');return null;},get 1(){throw Error('absent read');}};
    same(idl.argument(args,0).value,null);same(log.join(','),'length,value');
    log.length=0;same(idl.argument(args,1).present,false);same(log.join(','),'length');
  });
  for(const [label,input,unsigned,signed] of [['negative',-1,4294967295,-1],['fraction',-1.75,4294967295,-1],
    ['wrap',4294967297,1,1],['signedBoundary',2147483648,2147483648,-2147483648],
    ['unsignedBoundary',4294967295,4294967295,-1],['wrapNegative',-4294967297,4294967295,-1],
    ['nan',NaN,0,0],['infinity',Infinity,0,0],['negativeInfinity',-Infinity,0,0],
    ['negativeZero',-0,0,0],['undefined',undefined,0,0],['null',null,0,0],['string','3.75',3,3]]) {
    test('integer32.'+label,()=>{same(idl.integer(input,32,true),unsigned);same(idl.integer(input,32,false),signed);same(idl.unsignedLong(input),unsigned);same(idl.long(input),signed);});
  }
  test('integer16.boundaries',()=>{same(idl.integer(65535,16,true),65535);same(idl.integer(65535,16,false),-1);same(idl.integer(32768,16,false),-32768);same(idl.integer(-65537,16,true),65535);same(idl.integer(-0.5,16,false),0);});
  for(const convert of [v=>idl.integer(v,16,true),idl.unsignedLong,idl.long]) {
    const index=[...Object.keys(results)].length;
    test('numeric.rejections.'+index,()=>{
      for(const v of [1n,Symbol(),{valueOf(){return 1n;}},{valueOf(){return Symbol();}}]) throws(()=>convert(v),TypeError);
      const error={};same(throws(()=>convert({valueOf(){throw error;}}),Object),error);
    });
  }
  test('numeric.hintOrder',()=>{
    const log=[];same(idl.unsignedLong({valueOf(){log.push('valueOf');return {};},toString(){log.push('toString');return '-1';}}),4294967295);same(log.join(','),'valueOf,toString');
    log.length=0;same(idl.long({[Symbol.toPrimitive](hint){log.push(hint);return 2.9;}}),2);same(log.join(','),'number');
  });
  test('receiver.standardAndLegacy',()=>{
    class Interface {} const instance=new Interface(), forged=Object.create(Interface.prototype), brands=new WeakSet([instance]);
    const strict=idl.brandedReceiver(brands,'brand'),legacy=idl.legacyInstanceofReceiver(Interface,'legacy');
    same(strict(instance),instance);same(legacy(instance),instance);same(legacy(forged),forged);
    for(const value of [forged,{},null,undefined,1,new Proxy(instance,{})]) throws(()=>strict(value),TypeError,'brand');
    throws(()=>legacy({}),TypeError,'legacy');
  });
  test('receiver.policy',()=>{const check=idl.receiver(v=>v===null,'policy');same(check(null),null);throws(()=>check({}),TypeError,'policy');});
  test('descriptor.accessors',()=>{
    const target={},source={get value(){return 2;},set value(v){this.saved=v;}};
    const before=Object.getOwnPropertyDescriptor(source,'value');idl.accessors(target,source);
    const d=Object.getOwnPropertyDescriptor(target,'value');same(d.get,before.get);same(d.set,before.set);
    same(d.get.name,'get value');same(d.get.length,0);same(d.set.length,1);same(d.enumerable,true);same(d.configurable,true);
    throws(()=>Reflect.construct(d.get,[]),TypeError);target.value=3;same(target.saved,3);
  });
  test('descriptor.explicitFlags',()=>{
    const target={},proto={run(a,b){return a+b;}},get=function get(){return 4;};
    idl.accessor(target,'raw',{get,configurable:true,enumerable:false});same(Object.getOwnPropertyDescriptor(target,'raw').get,get);
    idl.readonly(target,'read',get,{configurable:false,enumerable:true});same(Object.getOwnPropertyDescriptor(target,'read').set,undefined);
    idl.method(target,'run',proto.run,{enumerable:true,configurable:true,writable:false});
    const d=Object.getOwnPropertyDescriptor(target,'run');same(d.value,proto.run);same(d.writable,false);same(d.value.name,'run');same(d.value.length,2);throws(()=>Reflect.construct(d.value,[]),TypeError);same(target.run(1,2),3);
  });
  test('errors.explicitConstructor',()=>{
    class SuppliedDOMException extends Error {constructor(message,name){super(message);this.name=name;this.code=11;}}
    const error=idl.domException(SuppliedDOMException,'InvalidStateError','public');
    assert(error instanceof SuppliedDOMException);same(error.name,'InvalidStateError');same(error.message,'public');same(error.code,11);
    const legacy=idl.namedError('InvalidStateError','legacy');assert(legacy instanceof Error);same(legacy.name,'InvalidStateError');same(legacy.message,'legacy');assert(!(legacy instanceof SuppliedDOMException));
  });
  test('prepareThenCommit.order',()=>{
    let state='old';const log=[];
    const a={toString(){log.push('a');return 'a';}},b={valueOf(){log.push('b');return 3;}};
    same(idl.prepareThenCommit(()=>[idl.domString(a),idl.long(b)],values=>{log.push('commit');state=values.join(':');return state;}),'a:3');same(log.join(','),'a,b,commit');
  });
  test('prepareThenCommit.failure',()=>{
    let state='old',sideEffects=0;const error={};
    same(throws(()=>idl.prepareThenCommit(()=>{sideEffects++;idl.domString({toString(){throw error;}});},()=>{state='new';}),Object),error);
    same(state,'old');same(sideEffects,1);
  });
  return JSON.stringify(results);
})(pocWebIDL);
