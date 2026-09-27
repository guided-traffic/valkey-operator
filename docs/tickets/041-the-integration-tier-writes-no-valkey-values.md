# T41 - The integration tier writes no Valkey values

> **Status:** analysed, waiting on the decision in Q1. Severity low, security none, urgency
> `later` (a cheap known fix), effort XS under Option A.

## Current state

The documentation describes the tiers correctly. `CLAUDE.md` (Testing) says envtest starts a
kube-apiserver and etcd and no kubelet, so nothing in the integration tier opens a Valkey
connection, and that writing values and verifying replication belongs to E2E, "the only one that
can". [ADR 0017](../adr/0017-test-and-ci-policy.md) D2
([`:225-233`](../adr/0017-test-and-ci-policy.md#L225-L233)) states the same split; its exclusive
clause for E2E is a conjunction: the only tier that writes values **and** verifies replication
([`:228-230`](../adr/0017-test-and-ci-policy.md#L228-L230)).
[`docs/developer/testing.md:15-16`](../developer/testing.md#L15-L16) says the same.

**What is missing:** the tree records the split, not the decision to keep it. ADR 0017
Alternatives Considered ([`:1135-1264`](../adr/0017-test-and-ci-policy.md#L1135-L1264)) has no
entry for "a real Valkey pair in the integration tier", so the question gets reopened by anyone
reading the old requirement. The Residual risks already record that the init container scripts
are not run inside the pinned images and that only the two pinned images are checked
([`:1271-1282`](../adr/0017-test-and-ci-policy.md#L1271-L1282)).

**Where the config `valkey-server` reads comes from.** `GenerateValkeyConf`
([`internal/builder/configmap.go:46`](../../internal/builder/configmap.go#L46)) writes the
ConfigMap text; the replica variant carries `replicaof` naming pod-0
([`configmap.go:33-39`](../../internal/builder/configmap.go#L33-L39)) or the known master
([`configmap.go:164-172`](../../internal/builder/configmap.go#L164-L172)). The init container
`init-config-selector` does not use that text verbatim on multi-replica pods:

- Sentinel branch ([`statefulset.go:275-356`](../../internal/builder/statefulset.go#L275-L356)):
  asks the Sentinels for up to 30 s, then takes the replica file's `replicaof`; the named pod copies
  the master file, every other pod copies the master file and appends `replicaof $MASTER_ADDR`
  (`:329-340`). The ordinal fallback (`:341-350`) is reachable only without a `replicaof` line in
  the replica file (by reading).
- Non-Sentinel branch ([`statefulset.go:440-544`](../../internal/builder/statefulset.go#L440-L544)):
  asks peers for up to 15 s, then self-claims or accepts the peer named by the replica file
  (`:490-519`); a found master is appended as `replicaof` (`:522-527`), otherwise self-claim or
  ordinal fallback (`:528-536`).
- Both branches append `replica-announce-ip`/`replica-announce-port` (`:352-356`, `:540-544`).
- Data-tier auth is on the command line (`--requirepass`, `--masterauth`,
  [`statefulset.go:822-834`](../../internal/builder/statefulset.go#L822-L834)), never in the file.

**What runs that config, per tier.**

| Tier | Real `valkey-server` | Config | Writes values | Verifies replication |
|---|---|---|---|---|
| Unit | no | runs the non-Sentinel data init ([`init_script_exec_test.go:96-119`](../../internal/builder/init_script_exec_test.go#L96-L119)) and the Sentinel pod init on the host shell with stubs; `GenerateValkeyConf` asserted by substring | no | no |
| Integration (envtest) | no, no kubelet and no StatefulSet controller ([`suite_test.go:63-65`](../../test/integration/suite_test.go#L63-L65)) | none | no | no |
| Imagetools (docker) | yes, both pins | hand-written flags ([`restricted_runtime_test.go:29-32`](../../test/imagetools/restricted_runtime_test.go#L29-L32)) | yes ([`:111`](../../test/imagetools/restricted_runtime_test.go#L111)) | no |
| E2E | yes, both pins, every PR | generated config plus init appends, in real pods | yes ([`e2e_test.go:273`](../../test/e2e/e2e_test.go#L273)) | yes ([`e2e_test.go:491`](../../test/e2e/e2e_test.go#L491)) |

No test below e2e executes the Sentinel-branch data init. `Valkey Image Tools` and `E2E Tests`
are both required status checks on `main`. Non-Sentinel replication is asserted in e2e
([`rolling_update_test.go:97`](../../test/e2e/rolling_update_test.go#L97),
`two_replica_failover_test.go:32`, `splitbrain_test.go:38`).

**Measured in docker** (both pins `9.1.1` and `8.1.9`, imagetools posture, configs transcribed by
hand from `configmap.go` and the non-Sentinel appends):

- A generated master/replica pair on a docker network with the generated FQDNs as aliases
  replicates: a value set on the master is read on the replica, `master_link_status:up`.
- An unknown directive is fatal at boot (`FATAL CONFIG FILE ERROR ... Bad directive`); in e2e it
  shows as a pod that never becomes Ready.
- A lone replica with an unresolvable `replicaof` boots as `role:slave`, link down.
- The TLS block without `/tls` files is fatal at boot.
- Persistence mode `both` (duplicate `dir /data`,
  [`configmap.go:203-243`](../../internal/builder/configmap.go#L203-L243)) boots. No test in any
  tier uses mode `both`.

**Impact.** No production path and no dataset is affected. Without a change, a directive a Valkey
release rejects is found as an e2e timeout instead of a named line in the imagetools job, and the
tier question stays open for the next reader.

## Required changes

Depends on the answer to Q1.

**Under Option A:**

- ADR 0017 Alternatives Considered: new entry "A real Valkey pair in the integration tier", plus
  a Status amendment line. The entry states:
  - why it lost: envtest has no kubelet, and a pair would duplicate the replication assertion the
    required `E2E Tests` context makes on the same pins in the same PR run;
  - imagetools already writes values into a single server, so D2's exclusive clause is
    replication;
  - Option C-prime as the upgrade path; the Sentinel-branch data init needs a D19 unit exec
    harness regardless;
  - the revisit trigger: a generated-config or config-writer defect reaches a cluster, or an e2e
    run fails because `valkey-server` rejects a generated directive;
  - persistence `both` boots on both pins (measured), since no test boots it;
  - a cross-reference to the two Residual risks at `:1271-1282` instead of restating them.
- It must not claim that Renovate bumps the Valkey pins (it does not, see T45).
- Verify: `grep -n 'A real Valkey pair in the integration tier' docs/adr/0017-test-and-ci-policy.md`
  finds the entry inside Alternatives Considered.

**Under Option C-prime:**

- New test in `test/imagetools`, per pin (`pinnedImages()`): run the generated
  `init-config-selector` in the image with no peer or Sentinel answering, the ConfigMap texts on
  tmpfs mounts, then boot `valkey-server` through the generated container command (auth variant
  with `VALKEY_PASSWORD`) plus a TLS variant with a certificate fixture. Assert boot, role and
  `master_host` from `INFO replication`. The discovered-master path (`statefulset.go:522-527`)
  needs a stub `valkey-cli` or a live peer.
- Update `CLAUDE.md` ("the config-writer scripts are not run there"),
  [`docs/developer/testing.md:157`](../developer/testing.md#L157), and ADR 0017 D53 (`:843`) with
  its residual risk (`:1271-1278`).
- Verify: `make test-image-tools` green on both pins; mutations recorded (a bad directive fails the
  boot with the line named; removing the `replicaof` append fails the role/`master_host`
  assertion); `make lint` and `make cyclo` clean.

## Open questions

### Q1: Does the generated Valkey configuration get a real-server check below e2e?

Today only e2e boots `valkey-server` on the generated config, on both pins, on every PR, behind a
required check. A rejected directive already turns that PR red; the choice decides whether it is
found within seconds in `Valkey Image Tools` with the line named, or as an e2e timeout. No option
below e2e tests the operator's reconciliation of the config, and replication stays E2E-only in
both.

- **A - keep the split and record it (recommended).** One ADR 0017 Alternatives entry; docs only,
  XS. A rejected directive keeps surfacing as an e2e pod that never becomes Ready.
- **C-prime - boot the file the init container writes, in `test/imagetools`.** Faster, named
  failure and the only check below e2e of the full file in the real image's shell; S-M effort, a
  certificate fixture, about 15 s and 30 s of backoff per script run unless shortened, three doc
  amendments. Should follow T35's rewrite of the script.

A is recommended because e2e already boots every generated directive on both pins as a required
check, the non-Sentinel replication defect class is already asserted there, and C-prime's main
value (catching a rejected directive on a pin bump) does not occur while the pins never move. Its
other gain, running the Sentinel-branch data init, comes cheaper from the D19 unit exec harness
T35 needs anyway; the revisit trigger in the A entry decides whether C-prime is built later.

**Answer:** _open_

## Not verified

- The measured configs were transcribed by hand, not rendered by `GenerateValkeyConf`; a test
  rendering through the builder would settle it.
- Sentinel-branch ordinal fallback being unreachable while the replica ConfigMap is mounted is by
  reading, not measured in a pod.
- C-prime's effort and the scripts' real backoff timing in the image are estimates.

## Related

- T35 - rewrites the Sentinel-branch data init and needs the D19 unit exec harness for it.
- T43 - tagged test files (including a C-prime test) are not vetted, linted or format-gated.
- T45 - Renovate never processes `test/testimages/images.go`, so the Valkey pins do not move.
