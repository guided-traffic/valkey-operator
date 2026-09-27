---
id: T23
title: "`pauseRollingUpdate` records no pause — it clears the state and re-arms a fresh budget"
state: filed
severity: low         # "low as a defect, medium as a documentation lie"
security: none
urgency: later
effort: L
blocked-by: adr-0010
filed-from: T15 D4
opened: 2026-08-26
decided:
done:
---

# T23 - `pauseRollingUpdate` records no pause — it clears the state and re-arms a fresh budget

**Severity: low as a defect, medium as a documentation lie. Status: open, filed 2026-08-26 by
T15 D4. Effort: L (~80 LOC plus an ADR 0010 re-decision).**

`pauseRollingUpdate` calls `clearRollingUpdateState`
([`rolling_update.go:2073`](../../internal/controller/rolling_update.go#L2073)), which deletes
`annotationSyncWaitStarted` and drops the in-memory wait bound. Nothing on the CR then records
that a pause happened except the `RollingUpdatePaused` condition, and the next dispatching pass
re-arms a **fresh** `syncTimeout` budget, waits it out and pauses again — re-emitting the
Warning Event each cycle. On a 5 min `syncTimeout` at a 10 s requeue that is a repeating cycle,
not a halt.

Two consequences:

* **It contradicts ADR 0010's own rule** that expiry hands over to another bounded state and
  *never* to a cleared rolling-update state — which is exactly what this does.
* **Four tracked sentences promise a halt.** T15 D4 corrects them in text; this item is the
  version where they become true at the mechanism: a `statePaused` state that both dispatchers
  hold until the generation changes.

**The re-decision it needs.** Is a state that ends only at a spec change *bounded* in ADR 0010's
sense, or is it the unbounded wait D3 refuses? That question is why this is its own item and not
part of T15: answering it either way is an ADR 0010 amendment, and coupling it to a status
lifecycle fix makes both unreviewable.

**Not verified:** the ~29-`False`-passes-per-`True` figure quoted in the T15 analysis is derived
from the requeue delay and the default `syncTimeout`, not observed on a cluster.

## History

- 2026-09-27 - extracted verbatim from the collection ticket (now [archive/039-findings-from-the-1-11-0-fleet-rollout.md](archive/039-findings-from-the-1-11-0-fleet-rollout.md)) into its own file when the tickets were numbered. Frontmatter filled from the final board row (board archive of that file, groomed 2026-09-26) and from the section text.
