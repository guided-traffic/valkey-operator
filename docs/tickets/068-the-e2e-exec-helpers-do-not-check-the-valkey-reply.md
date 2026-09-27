---
id: T68
title: the e2e exec helpers do not check the Valkey reply, so a refused write is not an error to them
state: analysed       # facts read and valkey-cli behaviour measured on both pins; D1 open
severity: low         # test code only; every silent write is read back later, so a refusal still fails its test, under a wrong diagnosis
security: none        # e2e helpers; no guarantee of the operator rests on them
urgency: now          # rule 1: three helper comments in tracked files state false behaviour
effort: S             # XS code under option B plus comment fixes; the full e2e run on both Valkey lines dominates
blocked-by: decision  # D1
filed-from: T12
opened: 2026-09-27
decided:
done:
---

# T68 - the e2e exec helpers do not check the Valkey reply, so a refused write is not an error to them

## Current state

The e2e tier talks to Valkey through `kubectl exec <pod> -- valkey-cli --raw -p <port> <args>`.
Two helpers are *strict* (a failure ends the test) and decide success only on the exit status of
`kubectl exec`:

- `valkeyExec` ([`e2e_test.go:225-268`](../../test/e2e/e2e_test.go), `--raw` at `:247`): five
  attempts with a linear delay of 4, 6, 8, 10 s (`:237`), then `require.NoError` (`:266`).
- `valkeyTLSExec` ([`tls_test.go:45-88`](../../test/e2e/tls_test.go), `--raw` at `:63`): three
  attempts 2 s apart (`:52-56`, `:86`).

`valkey-cli` without `-e` prints an error reply on stdout and exits 0 (measured on 9.1.1 and 8.1.9
for `NOREPLICAS`, `READONLY`, `OOM`, `LOADING`, `WRONGTYPE` and Sentinel `NOGOODSLAVE`). So a
refused write comes back to the caller as an ordinary string. With `-e`, the error reply goes to
stderr with exit 1, while `OK`, integers (including `TTL` `-1`/`-2`), an empty `GET`, `PING` and
`PUBLISH` still exit 0 (measured). A "stdout starts with `-`" rule does not work: `--raw` drops the
`-` of an error and `TTL` prints `-1`/`-2`. A connection refusal already exits 1 without `-e`.
`READONLY`, `OOM`, `NOREPLICAS` and `LOADING` are rejected in `processCommand` before execution,
so a refused write can be sent again safely.

The lenient helpers `valkeyExecAllowError`
([`standalone_test.go:550-596`](../../test/e2e/standalone_test.go)), `valkeyTLSExecAllowError`
([`tls_test.go:90-115`](../../test/e2e/tls_test.go)) and `valkeyExecQuick`
([`e2e_test.go:520-542`](../../test/e2e/e2e_test.go)) are meant to return error text; every test
that asserts a `READONLY` or `ERR` text uses one of them. They are not part of the fix.

**Call sites affected:**

- **15 writes discard the strict helper's reply:** [`tls_test.go:496-499`](../../test/e2e/tls_test.go),
  `:830`, `:836`, `:842`, `:1284`, `:1288`, `:1295`, `:1298`,
  [`tls_rotation_test.go:131`](../../test/e2e/tls_rotation_test.go),
  [`sidecar_test.go:481`](../../test/e2e/sidecar_test.go),
  [`rolling_update_test.go:291`](../../test/e2e/rolling_update_test.go),
  [`fleet_upgrade_test.go:768`](../../test/e2e/fleet_upgrade_test.go) (TLS branch of `fleetMSET`).
  Each is read back later, so no test passes because of the defect.
- **Two `SENTINEL FAILOVER` calls discard the reply:**
  [`sentinel_stale_master_test.go:80`](../../test/e2e/sentinel_stale_master_test.go) and
  [`sentinel_peer_table_test.go:92`](../../test/e2e/sentinel_peer_table_test.go). Sentinel answers
  `INPROG` or `NOGOODSLAVE`, today visible only as the following wait timing out. A `SENTINEL
  FAILOVER` sent again after the running failover ended starts a second failover (measured with
  one master and one replica: the master moved back), so it must not ride a retry after a reply.
- **20 strict calls sit inside polls** (11 `require.Eventually`, 7 `pollUntil`, 2
  `wait.PollUntilContextTimeout`), where an error reply today just makes the condition false; two
  of them go through the wrapper `authTLSExec`
  ([`pod_security_test.go:84-88`](../../test/e2e/pod_security_test.go)). Ten of the 20 send `INFO`,
  which `LOADING` never reaches.

**Three helper comments are false:**

- [`standalone_test.go:591`](../../test/e2e/standalone_test.go) says a `READONLY` reply is a
  non-zero exit; it exits 0 and leaves through the success return at `:579-581`.
- [`e2e_test.go:228`](../../test/e2e/e2e_test.go) says "Retries up to 5 times with exponential
  backoff"; it is five attempts with a linear 4, 6, 8, 10 s delay.
- [`e2e_test.go:520-522`](../../test/e2e/e2e_test.go) says `valkeyExecQuick` "returns \"\" on any
  error"; it does so only on an exec error, a Valkey error reply comes back as its text.

**Impact:** a refused write is silent at the write and misdiagnosed at the read-back, so triage
starts at the operator where there is nothing to find. Clearest cases: the rotation canary
(`tls_rotation_test.go:131`) fails as a canary lost across the certificate roll (`:221-222`); the
drain key (`sidecar_test.go:481`) fails as data lost across the replica deletion (`:499-500`); the
fleet TLS `MSET` fails at the `EXISTS` check as data lost by the operator upgrade
([`fleet_upgrade_test.go:397-416`](../../test/e2e/fleet_upgrade_test.go)). Refusals happen when a
write lands on a replica, a master being demoted, a loading pod, or a master under `maxmemory` or
`min-replicas-to-write`, which is exactly after the failovers and rolls the suite performs. Write
fencing ([T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md) option 1) refuses
writes by design and needs refused writes to be errors, so it depends on this ticket. The
operator's own Valkey client is not affected: it returns an error reply as a Go error
([`client.go:479-481`](../../internal/valkeyclient/client.go)).

## Required changes

### Independent of the open questions

- Correct the three comments above (`standalone_test.go:591`, `e2e_test.go:228`,
  `e2e_test.go:520-522`). Can land first.
- The retry log lines name the last error (`e2e_test.go:238`, `tls_test.go:54`), so a retried
  failure is identifiable in the run log.

### Depends on the answers

- Under B or A: `"-e",` next to `"--raw",` in `valkeyExec` (`e2e_test.go:247`) and `valkeyTLSExec`
  (`tls_test.go:63`) only, never in the lenient helpers.
- Under B or A: the two `SENTINEL FAILOVER` calls move to `valkeyExecAllowError` (no `-e`, retries
  only on a transport error) and assert the reply is `OK` or starts with `INPROG`.
- Under A only: the classifier, a poll-safe variant with an `authTLSExec` counterpart, the 20 poll
  sites moved to it, and the 11 `require.Eventually` sites among them converted to
  `wait.PollUntilContextTimeout` (ADR 0017 D25).
- Under D only: `require.Equal` on the expected reply at each of the 15 discarding sites.

### Verification

- Revert check (ADR 0017 D7) on Kind: point `sidecar_test.go:481` at a replica for the check. With
  the fix the test fails at the write with `READONLY` in the message; without `-e` it fails at the
  read-back (`:499-500`). Record both messages, restore the site.
- `grep -n '"-e",' test/e2e/e2e_test.go test/e2e/tls_test.go test/e2e/standalone_test.go` shows the
  flag only in the two strict helpers.
- `grep -n 'SENTINEL", "FAILOVER' test/e2e/*.go` shows both calls on `valkeyExecAllowError` with a
  reply assertion.
- `grep -n 'exponential\|e.g., Valkey READONLY\|on any error' test/e2e/e2e_test.go
  test/e2e/standalone_test.go` finds none of the false wordings.
- Full e2e suite green on both Valkey lines; grep the log for `Retrying valkeyExec` /
  `Retrying valkeyTLSExec` lines carrying a reply text, each a refusal the suite used to swallow.

## Open questions

### Q1: How does `valkey-cli -e` reach the two strict helpers?

With `-e` an error reply becomes exit 1, which `kubectl exec` passes through with the reply on
stderr. Today every non-zero exit is treated as a transport failure: retried, then "kubectl exec
failed". The choice is whether a reply error takes that path or is told apart and fails at once.

- **B - add `-e`, keep the retry path (recommended).** XS, two argv lines. A transient refusal
  (`LOADING`, `READONLY` just before a role flip) heals within the retries, which is safe because
  refused commands were not executed; a persistent one fails with the reply text in the message,
  after 28 s (4 s over TLS). The label "kubectl exec failed" is wrong for a reply error; inside polls
  a persistent error now ends the test from the helper instead of at the poll budget. Every strict
  command sent today is harmless to repeat after an error reply, except `SENTINEL FAILOVER`, which
  moves out. Residual rule: a command whose repetition has an effect does not go through a strict
  helper.
- **A - `-e` plus a classifier and a poll-safe variant.** S. Exit 1 with kubectl's `command
  terminated with exit code 1` and without `Could not connect to Valkey` fails at once, correctly
  labelled; never resends after a reply. Costs a classifier on two message texts this repository
  does not control, a second helper, and 20 call-site moves with 11 wait conversions in
  `//go:build e2e` files that lint and vet do not see
  ([T43](043-lint-and-vet-skip-every-build-tagged-test-file.md)).
- **D - assert the reply at the 15 sites, no helper change.** S. Fixes today's sites only; the
  next discarding write repeats the defect, and reads inside polls still take error text for an
  answer.

B closes the defect for every current and future strict call with two argv lines. A's gain is a
correct label and a faster failure on the failure path only, paid with foreign-text parsing and 20
call-site moves; no strict caller tests a refusal, so a write that lands after a retry is the state
the test wanted.

**Answer:** _open_

## Not verified

- On Kubernetes: that `kubectl exec` forwards `valkey-cli -e` stderr and exits 1 is read in
  client-go and kubectl source, not run. The revert check on Kind settles it.
- Whether any e2e run has ever hit a refused write at one of the 15 sites; no run log was searched.
- Where a second failover leaves the master among three pods at the two e2e call sites; measured
  only with one master and one replica.
- The `LOADING` reply to `INFO` during a load: the flag is measured, the reply is not.

## Related

- [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md): its option 1 (write
  fencing) needs this ticket landed first; on done, update T12's pointer.
- [T34](034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md): also edits
  `test/e2e/e2e_test.go` (different functions); one full e2e run on both lines can serve both.
- [T43](043-lint-and-vet-skip-every-build-tagged-test-file.md): the edited files carry
  `//go:build e2e`, which lint and vet skip.
