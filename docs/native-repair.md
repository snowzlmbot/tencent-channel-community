# Native notification repair: source-level gate

The requested target is the native CLI notification engine, not the independent interaction snapshot adapter. Do not activate the adapter as a substitute for this request.

## Verified blockers

- Current npm native CLI: 1.0.10; module `qqcli`, build gitHead `078c474fde6a9b26139acf5075bde15180b71939`.
- The official Skill repository has documentation, not the native Go implementation.
- npm metadata does not identify a source repository or license. The local package has no Go source, and no local qqcli module source was found.
- Official Skill repository has no releases at investigation time; the open compatibility issue has no maintainer response.
- The existing CLI route test fails on a retired legacy session JSON lookup. Altering that file is not a source-level fix.

## Required patch surface once source is available

1. Replace direct legacy session JSON discovery with a supported, owner-scoped conversation route resolver. Keep conversation/topic/account context intact, reject unknown or unauthorized sessions, and avoid reading transcripts.
2. Replace shell-based platform delivery (where present) with a supported authorized host integration. Source availability alone does not grant messaging authority; require an approved delivery capability and receipts.
3. Keep native interaction/system/direct-message pollers, source-specific baselines, same-second dedupe, retries and reference mapping. Do not substitute first-page snapshot hashes for native event identity.
4. Require test delivery and explicit user receipt confirmation before activating any route. Treat a successful CLI envelope with `status=test_failed` as failure.
5. Preserve existing credential storage, controlled egress and TLS verification. Do not export secrets, rewrite subscriptions by hand, inject a fake executable, downgrade session storage or hot-patch opaque binaries.

## Validation gate

Use a manually dispatched cloud workflow to test owner/session routing, topic isolation, authentication failure, receipt failure, two-step activation, duplicate/retry semantics and all supported notice sources. Credential-free fixtures do not establish real delivery. Final acceptance additionally needs an authenticated native test message, user confirmation, subscription status showing the exact authorized route, and a real notification lifecycle.

## Needed input

An authorized source archive/repository matching the native CLI build, with the applicable license/build instructions, or a vendor-supported native host delivery integration. Neither a documentation fork nor extra permissions over local files supplies the missing implementation.
