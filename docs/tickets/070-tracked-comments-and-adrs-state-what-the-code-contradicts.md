---
id: T70
title: tracked comments, ADRs and pages state what the code and the measurements contradict
state: analysed
severity: low         # no behaviour is wrong; the worst record, ADR 0012 D9, hides a measured write loss
security: hardening   # items (d), (e) and (k) are statements in the security records; no principal gains or loses anything
threat: "no attacker; a reviewer of the threat model reads the password exposure on the wrong process and a sidecar Role wider than the code grants, while every real carrier is readable only inside a container that already holds VALKEY_PASSWORD."
urgency: now          # rule 1: measured-false statements in tracked files
effort: S             # sentence-level edits in comments, ADRs and pages; no behaviour or test change
blocked-by: human     # only the CLAUDE.md edit of item (g); everything else can land now
filed-from: re-verification of the open tickets, moved from tickets 012, 034, 050, 056, 057, 058 and 060
opened: 2026-09-27
decided:
done:
---

# T70 - tracked comments, ADRs and pages state what the code and the measurements contradict

One work list of false or stale sentences in tracked files. Every item corrects text, none changes
behaviour, none depends on another.

## Current state

**(a) "no replicas attached yet" holds only for `replicas: 2`.** `promoteAndRedirect`
([rolling_update.go:4188-4249](../../internal/controller/rolling_update.go#L4188-L4249)) demotes
the old master to a replica of the promoted pod and redirects every other replica to it before the
old master is deleted, so with 3+ replicas the promoted pod has replicas attached (a replica still
syncing already counts toward `connected_slaves`) and the init script's Phase 1
([statefulset.go:457-488](../../internal/builder/statefulset.go#L457-L488)) can find it. Two
comments say it never can: [statefulset.go:490-496](../../internal/builder/statefulset.go#L490-L496)
(Phase 2 comment in the script) and [rolling_update.go:4039-4043](../../internal/controller/rolling_update.go#L4039-L4043).
Phase 2 stays load-bearing on larger clusters (no redirected replica attached yet, or a failed
best-effort redirect), so only the scope of the sentence changes. The shell comment is in the
init container's `Command`, so editing it moves the pod-spec hash of every multi-replica data tier
without Sentinel; that tier rolls on every operator release anyway (sidecar runs the operator
image), and [ADR 0005 D11](../adr/0005-upgrade-neutral-defaults-and-anti-affinity.md) lets such a
change ride the release roll without a release note.

**(b) `WAIT` does not drain a master of writes.** `waitForWriteSync`
([rolling_update.go:2901-2977](../../internal/controller/rolling_update.go#L2901-L2977)) covers
only its own connection's writes; writes acknowledged between `WAIT` and the demotion are not on
the promoted pod. Measured in docker (M1) on 9.1.1 and 8.1.9: 1 of about 1500 acknowledged keys
lost per sequence at about 200 writes/s. Four places claim otherwise:
[rolling_update.go:4216-4217](../../internal/controller/rolling_update.go#L4216-L4217) ("drained of
writes by waitForWriteSync"), [ADR 0012 D9](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md)
lines 340-341 ("It loses no data"), the `waitForWriteSync` doc comment at `:2901-2903` ("prevents
data loss") and the Sentinel-path call site at `:2715-2716` ("prevents data loss from async
replication").

**(c) `sentinelPodNeedsUpdate` doc comment starts mid-sentence**
([rolling_update.go:4837](../../internal/controller/rolling_update.go#L4837)). The function decides
on four inputs (`:4838-4873`): container image by name, the `pod-spec-hash` annotation (resource
comparison when absent), the `config-hash` annotation, the TLS material fingerprint.

**(d) The sidecar Role is recorded wider than granted.** `BuildSidecarRole` grants `pods` `get`
and `patch` restricted by `resourceNames` to this cluster's data-pod names, and no rule when the
list is empty ([rbac.go:58-121](../../internal/builder/rbac.go#L58-L121)).
[ADR 0013 D3](../adr/0013-operator-is-cluster-wide-privileged.md) lines 167-168 say `pods:
get,list,patch` in one namespace (its strict-subset conclusion holds).
[ADR 0012 Consequences](../adr/0012-the-sidecar-records-its-drain-promotion-on-the-pod.md) lines
451-460 state in present tense that a sidecar can patch another cluster's pods and the observer
token carries namespace-wide `pods get,list,patch`; ADR 0012's Status already records D8 steps 2
and 3 as shipped, but those bullets are not marked superseded.

**(e) ADR 0016 and the secrets page name the wrong `/proc` carrier of the password.**
[ADR 0016](../adr/0016-authentication-and-tls-posture.md) lines 195-198 and
[secrets-and-tls.md:31-35](../security/secrets-and-tls.md) put it in `valkey-server`'s argv.
Measured (M2) on both pins:

1. `valkey-server` keeps no copy: `/proc/1/cmdline` reads `valkey-server *:6379`, `/proc/1/environ`
   holds none (`set-proc-title yes`, the default).
2. `valkey-cli` does, while it runs: the probes
   ([statefulset.go:1515-1531](../../internal/builder/statefulset.go#L1515-L1531)) and the init
   discovery calls (`cliAuthFlags`, `:265-268` on Sentinel topologies with Sentinel auth enabled,
   `:430-433` without Sentinel) pass `-a "$VALKEY_PASSWORD"`.
3. `/etc/valkey-active`, the writable config copy on topologies with an init container
   ([statefulset.go:645-663](../../internal/builder/statefulset.go#L645-L663), `:232-238`), holds
   `requirepass` and `primaryauth` in cleartext after a `CONFIG REWRITE`. Sentinel sends one after
   every `REPLICAOF`; the operator and sidecar never do. ADR 0016 lines 199-200 name only
   `sentinel.conf`.

"Inside the pod" is also wrong: no generated pod shares its process namespace, so `/proc` shows a
process only to its own container. Every carrier sits in a container that already has
`VALKEY_PASSWORD` in its environment, so the correction names no new reader.

**(f) ADR 0026 says the drain hook exists on every topology.**
[ADR 0026 Context](../adr/0026-a-pod-being-deleted-is-not-available.md) lines 164-170. The hook
exists only when `IsMultiReplicaWithoutSentinel()`
([statefulset.go:678-680, :746-749](../../internal/builder/statefulset.go#L746-L749)); elsewhere
termination is `valkey-server`'s own shutdown within the 75 s grace period.

**(g) The required checks live in a ruleset, not in branch protection.**
`gh api repos/guided-traffic/valkey-operator/branches/main/protection` answers 404. The repository
ruleset `main` (id 23985346, active, default branch), read with
`gh api repos/guided-traffic/valkey-operator/rules/branches/main`, has `deletion`,
`non_fast_forward` and `required_status_checks` with exactly ADR 0017 D47's twelve contexts
(bound to the `github-actions` app, not strict); bypass `OrganizationAdmin` and `Integration`
5070048, both `always`; no review rule. Stale: [ADR 0017](../adr/0017-test-and-ci-policy.md) D47
(line 539), Consequences (1102-1104), Residual risks (1310-1317, including `enforce_admins`, a
classic-protection setting `main` does not have), [DEVELOPER.md:298-302](../../DEVELOPER.md)
("could not be read back"), [release.yml:482](../../.github/workflows/release.yml#L482) and
[CLAUDE.md:392-393](../../CLAUDE.md).

**(h) ADR 0017 D52 pins a stale version** (line 835: v0.37.0); [go.mod](../../go.mod) has
`k8s.io/api` and `k8s.io/pod-security-admission` at v0.37.1. The rule holds. Other v0.37.0 mentions
record what something was read in and stay.

**(i) The isolation page misdescribes ADR 0020.**
[isolation-and-tenancy.md:192-193](../security/isolation-and-tenancy.md) says ADR 0020 leaves out
the name filter; [ADR 0020](../adr/0020-write-only-what-the-operator-owns.md) (Status 18-25, Context
171-175, Alternatives 701-702) already says "the label set plus a name of the `<cr>-<ordinal>` form".

**(j) The `ExecMulti` doc comment says it returns the last error**
([client.go:330-332](../../internal/valkeyclient/client.go#L330-L332)). The loop (`:348-357`)
returns on the first failed write, read or `-` reply and sends nothing after it, as
`TestExecMulti_StopsAtTheFirstRejectedCommand` ([exec_test.go:252](../../internal/valkeyclient/exec_test.go#L252))
pins. No `MULTI`/`EXEC` is sent, so it is not atomic. The only production caller, the observer
write test ([checks.go:110-116](../../internal/observer/checks.go#L110-L116)), is unaffected.

**(k) The trust-boundaries diagram names a ClusterRole the chart never creates.**
[trust-boundaries.md](../security/trust-boundaries.md) line 24 labels it `valkey-operator-role`;
the chart names ClusterRole and binding `valkey-operator.fullname`
([clusterrole.yaml:4](../../deploy/helm/valkey-operator/templates/clusterrole.yaml#L4)).
`valkey-operator-role` exists only in the generated [role.yaml:5](../../config/rbac/role.yaml#L5).

**Impact.** (b) matters most: a contributor reading ADR 0012 D9 before touching
`promoteAndRedirect` learns the roll is lossless where it silently loses the writes acknowledged
between promote and demote. (a) invites removing or over-trusting Phase 2; (e) makes moving the
password off `valkey-server`'s command line (already off) look like a fix; (d) overstates the RBAC
footprint; (g) sends the next gate-job author to an endpoint where the change has no effect; (j)
misleads a new `ExecMulti` caller. (c), (f), (h), (i), (k) mislead a reader without a decision
resting on them.

**M1 (items a, b).** Docker, `valkey/valkey:9.1.1` and `8.1.9`: `m`, `r1`, `r2` with
`valkey-server --save '' --appendonly no`, r1 and r2 `REPLICAOF m 6379`. Writer inside `m`:
`i=0; : > /data/ack; while [ $i -lt 100000 ]; do r=$(valkey-cli SET load:$i $i); [ "$r" = OK ] && echo $i >> /data/ack; i=$((i+1)); done`.
After 3 s `WAIT 2 1000` on `m` (answers 2), then from r1:
`valkey-cli REPLICAOF NO ONE; valkey-cli -h m REPLICAOF r1 6379; valkey-cli -h r2 REPLICAOF r1 6379`.
After 4 s check the acknowledged indexes with `sed 's/^/EXISTS load:/' ack | valkey-cli` on r1:
r1 is `role:master`, `connected_slaves:2`, 1 key missing on both pins.

**M2 (item e).** Docker, both pins, `--user 999:999`, `valkey-server` started via
`sh -c 'exec valkey-server <conf> --requirepass "$VALKEY_PASSWORD" --masterauth "$VALKEY_PASSWORD"'`;
read `/proc/1/cmdline` and `/proc/1/environ`, scan `/proc/*/cmdline` during a background
`valkey-cli -a ... BLPOP`, read the config after `CONFIG REWRITE`.

## Required changes

ADR edits strike the false sentence in place with a dated correction and add a dated "correction,
no decision changes" line to `## Status` naming what was corrected and how it was verified. No ADR
index State changes.

**Can land now:**

1. **(a)** Scope [rolling_update.go:4039-4043](../../internal/controller/rolling_update.go#L4039-L4043)
   and the shell comment at [statefulset.go:490-496](../../internal/builder/statefulset.go#L490-L496):
   discovery rejects the promoted pod when no replica is attached - always with `replicas: 2`, on
   larger clusters until a redirected replica attaches or when a redirect failed. No release note.
2. **(b)** Rewrite `:4216-4217`: the demotion is best-effort and writes acknowledged after `WAIT`
   are lost. Scope `:2901-2903` and `:2715-2716` to "writes acknowledged before the call" (drop
   `:2715-2716` if T67 rewrites it first). Strike ADR 0012 D9's "It loses no data" and state the
   measured gap with the M1 method.
3. **(c)** Restore a complete `sentinelPodNeedsUpdate` doc comment naming its four inputs.
4. **(d)** ADR 0013 D3: `pods: get, patch` restricted by `resourceNames` to this cluster's data-pod
   names, keep the subset conclusion (T82 edits the same paragraph; the second to land rebases).
   Mark both ADR 0012 Consequences bullets superseded by D8 steps 2 and 3, pointing at
   [privilege-footprint.md](../security/privilege-footprint.md#the-per-instance-sidecar-role).
5. **(e)** ADR 0016 and secrets-and-tls.md: the password is in each probe's and init step's
   `valkey-cli` argv while it runs, visible in `/proc` of that container only, not in
   `valkey-server`'s argv; add `/etc/valkey-active` (cleartext after `CONFIG REWRITE`, on Sentinel
   topologies after every Sentinel `REPLICAOF`) next to `sentinel.conf`. Keep the page's
   conclusion.
6. **(f)** ADR 0026 Context: scope the one-second release and the 60 s cap to multi-replica
   clusters without Sentinel; the other topologies have no drain hook.
7. **(g)** ADR 0017 D47, Consequences, Residual risks, DEVELOPER.md and release.yml:482: name the
   ruleset `main` (id 23985346), its `gh api .../rules/branches/main` read-back matching D47, and
   the bypass list in place of `enforce_admins`. The earlier API-call sentence stays as dated
   history.
8. **(h)** ADR 0017 D52: drop the number - "sits at the same version as `k8s.io/api`; the
   `k8s-go-modules` group moves both".
9. **(i)** isolation-and-tenancy.md:192-193: say ADR 0020 now reads the same way, or drop the
   sentence.
10. **(j)** `ExecMulti` doc comment: sequential on one connection, the first failure ends the
    sequence and is returned, later commands are not sent, no `MULTI`/`EXEC`, not atomic. No
    rename.
11. **(k)** trust-boundaries.md line 24: replace `valkey-operator-role` with `<release>`, keeping
    the box width.

**Needs Hans:**

12. **(g)** CLAUDE.md:392-393: "adding it to branch protection" becomes "adding it to the required
    status checks of the repository ruleset `main`".

**Verification:**

- `git grep -n 'no replicas attached yet'` outside tests finds only scoped sentences.
- `git grep -n 'drained of writes\|drained the pod of writes\|It loses no data'`,
  `git grep -n 'last non-OK error'` and `git grep -n valkey-operator-role -- docs/` find nothing
  outside `docs/tickets/`.
- `git grep -n -i 'branch protection'` outside `docs/tickets/` finds only dated history (and
  CLAUDE.md until item 12 lands).
- ADR 0013 `get,list,patch` and the `valkey-server` argv claim appear only as struck text; ADRs
  0012, 0013, 0016, 0017 and 0026 carry a Status line.
- `git diff -U0 -- internal/ .github/` touches only comment lines, the `#` lines only in the Phase 2
  comment; `make lint` and `make test-unit` green. No mutation check applies.

## Not verified

- The item (b) loss and Phase 1 hit rate on Kubernetes under real write load; an e2e run settles it.
- Whether an exec probe's command line is visible outside the container (kubelet log, runtime
  events); reading or measuring on a node settles it.
- Which app actor 5070048 is, and whether a non-org-admin repository admin can bypass the ruleset;
  the ruleset settings in GitHub settle it.

## Related

- T67 - owns the Sentinel-path loss after `WAIT` and its "lossless" statements.
- T82 - edits the same ADR 0013 D3 paragraph as item (d).
