// Public, offline probe shared unchanged with pinned jsdom. No wall-clock
// assertions: completion depends on callback order and explicit cancellation.
(function() {
  const assert = (value, message) => { if (!value) throw Error(message); };
  const throws = fn => {
    let ok = false;
    // jsdom's Window timer methods throw from its Node realm. Compare the
    // exception interface name across realms rather than constructor identity.
    try { fn(); } catch (e) { ok = e.name === 'TypeError'; }
    assert(ok, 'timer conversion must throw TypeError');
  };
  for (const [name, length] of [['setInterval', 1], ['clearInterval', 0]]) {
    const d = Object.getOwnPropertyDescriptor(window, name);
    assert(d.value === window[name] && d.writable && d.enumerable && d.configurable, name + ' descriptor');
    assert(typeof window[name] === 'function' && window[name].length === length, name + ' signature');
    assert(typeof window[name].call === 'function', name + ' callable method');
  }
  throws(() => setInterval(Symbol(), 0));
  throws(() => setInterval(() => {}, Symbol()));
  throws(() => setInterval(() => {}, 1n));
  throws(() => clearInterval(Symbol()));
  throws(() => clearInterval(1n));
  let conversions = '';
  const converted = setInterval({toString() { conversions += 'h'; return 'void 0'; }},
    {valueOf() { conversions += 'd'; return 0; }});
  assert(conversions === 'hd', 'handler conversion precedes delay conversion');
  assert(clearInterval(converted) === undefined, 'clear result');
  // Missing/undefined delay uses zero; no timer is executed by these checks.
  const defaults = setInterval(() => {});
  clearTimeout(defaults);

  return new Promise((resolve, reject) => {
    let count = 0, cancelledFired = false;
    const marker = {};
    const cancelled = setInterval(() => { cancelledFired = true; }, 0);
    clearTimeout(cancelled);
    const timeout = setTimeout(() => { cancelledFired = true; }, 0);
    clearInterval(timeout);
    // 2^32 wraps to zero under WebIDL long conversion.
    const interval = setInterval(function(a, b) {
      try {
        assert(this === window && a === marker && b === 7, 'callback receiver and arguments');
        count++;
        if (count === 3) {
          clearTimeout(interval);
          setTimeout(() => {
            try {
              assert(count === 3 && !cancelledFired, 'repeat and shared cancellation');
              resolve('timer probe passed');
            } catch (e) { reject(e); }
          }, 0);
        }
      } catch (e) { clearInterval(interval); reject(e); }
    }, 4294967296, marker, 7);
    assert(Number.isInteger(interval) && interval > timeout && timeout > cancelled, 'shared handle pool');
    assert(count === 0, 'interval must be asynchronous');
  });
})()
