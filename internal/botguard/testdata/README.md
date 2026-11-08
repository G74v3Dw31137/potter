# BotGuard fixtures

`protocol-mock.js` is the original authored synthetic interpreter, consumed by
the original bundled BgUtils. It verifies callback order, exact binding bytes,
orchestration, caching, errors and lifecycle. It produces no valid Google proofs.
Other JS probes and JSON expectation files exercise the virtual browser offline.
The Go checks retain stored jsdom observations without requiring a Node runtime.

Private Google captures are not distributed. Offline regression coverage uses
synthetic protocol inputs and public browser observations; historical private
capture loaders, replay tapes and tracing hooks have been removed.

Use the explicitly opt-in TestLiveTokens at the module root for a bounded real
initialization. See the root README for invocation and evidence limits. Never
commit visitor identities, challenge captures, proofs, credentials or media URLs.
