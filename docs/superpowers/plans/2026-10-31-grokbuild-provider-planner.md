# Grok Build provider declaration implementation plan

Goal: implement the typed server-only Grok Build planner from `docs/superpowers/specs/2026-10-31-grokbuild-provider-planner.md` without pretending it is already a native apply or the entire F12.

Architecture: shared secret-free typed DTOs; existing registry manifest/digest validation; actual moejs module; independent bounded Go reconstruction/equality. Existing SQLite schemas, transport, other agents and all client runtimes remain unchanged.

1. Add a compiling metadata regression using existing Registry APIs; old Builtins must ordinary-FAIL because Grok is missing. Record RED.
2. Add shared typed DTOs/constants and minimal registry/module wiring. Extend the existing complete builtin assertion from five to six, preserving exact old-agent checks and adding Grok's exact capability/target/version assertions.
3. Add literal specification-oracle tests for native, custom, key/env/both, numeric bounds, malformed input, identity and module output. A compiling planner stub returns Unsupported and must ordinary-FAIL the positive tests. Record RED.
4. Implement validation, defensive cloning, real JS execution and independent exact output checking. Run focused tests, then prior agents and configuration callers. No secret values or source documents go to JS.
5. Compile/run output-check bypass and corrupt-builtin controls against unchanged assertions; ordinary RED required, restore production source and GREEN. Run shuffle, vet, module verify, dependency guard and CGO0 builds. Preserve any failures without guessing causes.
6. Freeze source8, source identity and executed evidence; obtain non-author Spec/Quality review before shared integration. Full fourteen-agent/plugin/native obligations stay open, with next asset/domain/RPC/UI/client phases explicitly retained. Only the controller commits/pushes accepted source.
