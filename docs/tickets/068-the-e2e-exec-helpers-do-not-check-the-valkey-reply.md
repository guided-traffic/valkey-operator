---
id: T68
title: the e2e exec helpers do not check the Valkey reply, so a refused write is not an error to them
state: analysed       # facts re-read and the valkey-cli behaviour measured on both pins at 84a39c2 (2026-09-27); one decision open, D1
severity: low         # test code only; each of the 15 silent writes is read back later, so a refusal still fails its test, but under a wrong diagnosis (Impact)
security: none        # e2e helpers; no guarantee of the operator rests on them
urgency: now          # rule 1, a measured-false statement in tracked files: the comment at test/e2e/standalone_test.go:591 names "Valkey READONLY" as an error that makes kubectl exec exit non-zero, and valkey-cli without -e exits 0 on it (measured, both pins); the doc comments at e2e_test.go:228 ("exponential backoff", the code is linear) and :520-522 (valkeyExecQuick "returns \"\" on any error") are false as well (Mechanism). Changed from later by the adversarial review of 2026-09-27 (History)
effort: S             # the code is XS under the recommended option B (S under option A), plus three comment corrections and two SENTINEL FAILOVER call sites; the full e2e suite on both Valkey lines and the revert check on Kind dominate
blocked-by: decision  # D1, below
filed-from: T12 (section "E2E impact", Work list item "The e2e reply check", Measurements), during the re-verification of 2026-09-27 at 84a39c2
opened: 2026-09-27
decided:
done:
---

# T68 - the e2e exec helpers do not check the Valkey reply, so a refused write is not an error to them

Filed on 2026-09-27 from [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md)
(section "E2E impact", the Work list item "The e2e reply check" and its Measurements row) during
the re-verification at `84a39c2`. The finding shares neither mechanism nor decision with write
fencing, so under the filing rule it is a ticket of its own. ~~T12 records the move in words ("moves
to a ticket of its own", section "E2E impact"; its prerequisite 4) but does not link this file yet
(checked 2026-09-27: no `T68` and no `068` in T12).~~ *(Sweep 2026-09-27: T12 now links this file
from its section "E2E impact", its prerequisite 4, its Work list and its Related tickets.)*
Everything below was re-read or re-measured at
`84a39c2` for this file.

## Fact

### Mechanism

The e2e tier talks to Valkey through `kubectl exec <pod> -- valkey-cli --raw -p <port> <args>`.
Two helpers are *strict*, meaning a failure ends the test:

- `valkeyExec` ([`e2e_test.go:225-268`](../../test/e2e/e2e_test.go), argv at `:243-249`, `--raw`
  at `:247`) runs the command up to five times, sleeping 4, 6, 8 and 10 s between attempts
  (`delay := time.Duration(attempt) * 2 * time.Second`, `:237`; 28 s of sleep plus five exec runs),
  and ends with `require.NoError(t, lastErr, ...)` (`:266`).
- `valkeyTLSExec` ([`tls_test.go:45-88`](../../test/e2e/tls_test.go), `--raw` at `:63`) does the
  same with three attempts 2 s apart (`:52-56`, `:86`).

Both decide success on **one signal only: the exit status of `kubectl exec`**
(`if err == nil { return strings.TrimSpace(stdout.String()) }`, `e2e_test.go:259-261`,
`tls_test.go:79-81`). `valkey-cli` without `-e` prints an error reply **on stdout and exits 0**
(measured on both pins, [Measurements](#measurements-2026-09-27-at-84a39c2)), so a write refused
with `READONLY`, `LOADING`, `OOM`, `NOREPLICAS` or `WRONGTYPE` returns to the caller as an
ordinary string. A caller that asserts the reply sees the error text in its assertion; a caller
that discards the reply sees nothing.

The three lenient helpers are meant to return error text and are not part of this finding:
`valkeyExecAllowError` ([`standalone_test.go:550-596`](../../test/e2e/standalone_test.go)),
`valkeyTLSExecAllowError` ([`tls_test.go:90-115`](../../test/e2e/tls_test.go)) and
`valkeyExecQuick` ([`e2e_test.go:520-542`](../../test/e2e/e2e_test.go), returns `""` on any exec
error, used inside polls). Every assertion on a `READONLY` or `ERR` text in `test/e2e` goes through
one of them: [`tls_test.go:798-802`](../../test/e2e/tls_test.go), `:1392-1395`, `:2025-2026` and
[`sentinel_stale_master_test.go:204-206`](../../test/e2e/sentinel_stale_master_test.go).

**Three comments on these helpers state the same misconception, and are false.** *(Found by the
adversarial review of 2026-09-27.)*

- [`standalone_test.go:591`](../../test/e2e/standalone_test.go), in `valkeyExecAllowError`:
  "For non-transient errors (e.g., Valkey READONLY), return the output as-is." The line is
  reached only when `cmd.Run()` returned an error (`:579-581` returns on success), so it states
  that a `READONLY` reply makes `kubectl exec` exit non-zero. Without `-e`, `valkey-cli` exits 0
  on `READONLY` (measured, both pins), so a `READONLY` reply leaves through the success return at
  `:579-581`, never through this line. The helper still returns the text, so its behaviour is
  right; the comment is not.
- [`e2e_test.go:228`](../../test/e2e/e2e_test.go), the doc comment of `valkeyExec`: "Retries up to
  5 times with exponential backoff". The delay is `attempt * 2 s` (`:237`), linear: 4, 6, 8, 10 s,
  and five attempts are four retries (read in code).
- [`e2e_test.go:520-522`](../../test/e2e/e2e_test.go), the doc comment of `valkeyExecQuick`:
  "returns \"\" on any error". It returns `""` on an exec error only (`:538-540`); a Valkey error
  reply exits 0 and comes back as its text (measured behaviour of `valkey-cli` without `-e`).

The first and the third are measured-false statements in tracked files, which makes the urgency
`now` by rule 1 (frontmatter); the second is false by reading the code. None of the three changes
under the fix of the strict helpers unless it is edited, so their correction is a Work list item
of its own.

**The detector is `valkey-cli -e`, not a `-` prefix rule.** `--raw` prints an error reply without
its leading `-` (stdout reads `NOREPLICAS Not enough good replicas to write.`), and integer replies
such as `TTL` print `-1` or `-2`, so a rule "stdout starts with `-`" would miss every error and
misfire on those (measured). With `-e`, `valkey-cli` in non-interactive mode writes the error reply
to stderr and exits 1
([valkey-cli.c 9.1.1:2229-2232](https://github.com/valkey-io/valkey/blob/9.1.1/src/valkey-cli.c#L2229-L2232);
the same branch at [8.1.9:2228](https://github.com/valkey-io/valkey/blob/8.1.9/src/valkey-cli.c#L2228);
the flag parsed at [9.1.1:2714-2715](https://github.com/valkey-io/valkey/blob/9.1.1/src/valkey-cli.c#L2714-L2715)),
while `OK`, integers, an empty `GET`, `PING` and `PUBLISH` still exit 0 (measured).

**A refused command was not executed, so it can be sent again.** `READONLY`, `OOM`, `NOREPLICAS`
and `LOADING` are rejected inside `processCommand` before `call()`
([server.c 9.1.1:4497](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L4497) `OOM`,
[`:4542`](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L4542) `NOREPLICAS`,
[`:4549`](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L4549) `READONLY`,
[`:4580`, `:4586`](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L4580-L4586) `LOADING`,
`call(c, flags)` at [`:4637`](https://github.com/valkey-io/valkey/blob/9.1.1/src/server.c#L4637);
on 8.1.9 at `server.c:4233`, `:4278`, `:4285`, `:4314`, `:4320`, `call` at `:4368`). Option B
below rests on this.

### Where it bites: the call sites

- **15 write sites discard the reply of a strict helper** (statement position), 11 of them in
  `tls_test.go`: [`tls_test.go:496-499`](../../test/e2e/tls_test.go), `:830`, `:836`, `:842`,
  `:1284`, `:1288`, `:1295`, `:1298`,
  [`tls_rotation_test.go:131`](../../test/e2e/tls_rotation_test.go),
  [`sidecar_test.go:481`](../../test/e2e/sidecar_test.go),
  [`rolling_update_test.go:291`](../../test/e2e/rolling_update_test.go) and
  [`fleet_upgrade_test.go:768`](../../test/e2e/fleet_upgrade_test.go) (the TLS branch of
  `fleetMSET`; the plain branch goes through `valkeyMSET`, which asserts `OK` at
  [`e2e_test.go:281`](../../test/e2e/e2e_test.go)). Every one of the 15 was re-read at `84a39c2`.
- **Each of the 15 is read back later**, so none lets a test pass: `tls_test.go:502-505`,
  `:833`, `:839`, `:845`, `:1332-1346`, `tls_rotation_test.go:221`, `sidecar_test.go:499`,
  `rolling_update_test.go:423`, and for the fleet the `DBSIZE` and `EXISTS` checks at
  [`fleet_upgrade_test.go:397-416`](../../test/e2e/fleet_upgrade_test.go).
- **Two more discarding calls are not writes:** `SENTINEL FAILOVER` at
  [`sentinel_stale_master_test.go:80`](../../test/e2e/sentinel_stale_master_test.go) and
  [`sentinel_peer_table_test.go:92`](../../test/e2e/sentinel_peer_table_test.go). Sentinel
  answers it with `-INPROG Failover already in progress` or `-NOGOODSLAVE No suitable replica to
  promote` ([sentinel.c 9.1.1:3941, :3945](https://github.com/valkey-io/valkey/blob/9.1.1/src/sentinel.c#L3941-L3945)),
  which today is discarded and surfaces only as the following wait timing out
  (`sentinel_peer_table_test.go:94-98`, "Sentinel must still be able to elect a leader and
  promote"). *(New at `84a39c2`. Measured by the adversarial review on both pins, Measurements:
  `NOGOODSLAVE` prints on stdout with exit 0 without `-e` and on stderr with exit 1 with `-e`,
  `INPROG` exits 1 with `-e` (without `-e` not measured, the same `valkey-cli` path); and once the
  failover in progress has ended, the same `SENTINEL FAILOVER` is accepted again and starts a
  second failover, which in the two-node measurement moved the master back to where it was.)*
  **So `SENTINEL FAILOVER` is not safe to retry:** an `INPROG` retried by the helper after the
  running failover has finished triggers another one. Neither call site is in a retry loop of its
  own today; the helper's retry is the only one (Options).
- **All write sites together:** the T12 audit counted 61 in 16 files through six helpers
  (`valkeyExec` 23, `valkeyTLSExec` 21, `valkeyMSET` 12, `valkeyExecAllowError` 2,
  `valkeyTLSExecAllowError` 2, `valkeyExecQuick` 1). A recount for this file, with its own script
  (calls of the six helpers whose arguments carry a write-verb literal or forward `args...`),
  found 64 hits in 17 files, of which four are not call-site writes (the forward inside
  `valkeyMSET` at `e2e_test.go:280`, the two forwards of the fleet `exec` wrapper at
  `fleet_upgrade_test.go:146`, `:148`, and the forensics reads of `valkeyPodForensics` at
  `e2e_test.go:699`), leaving 60. The difference of one to the audit is the verb list (`CONFIG SET`
  matches `SET`) and was not reconciled. The 15 discarding sites are identical, site by site, in
  both counts; that is the number the fix rests on. The strict helpers are called 183 times in
  `test/e2e` altogether, reads included (`grep -c 'valkeyExec(t\|valkeyTLSExec(t'`).
- **20 strict-helper calls sit inside a polling loop**, where an error reply today makes the
  condition false and the loop tries again: 11 in `require.Eventually`
  ([`sidecar_test.go:265`](../../test/e2e/sidecar_test.go),
  [`standalone_test.go:334`](../../test/e2e/standalone_test.go), `:344`, `:372`, `:408`,
  [`tls_test.go:1220`](../../test/e2e/tls_test.go), `:1303`, `:1318`, `:1353`, `:1374`,
  [`fleet_upgrade_test.go:750`](../../test/e2e/fleet_upgrade_test.go)), 7 in `pollUntil`
  ([`pod_security_test.go:179`](../../test/e2e/pod_security_test.go), `:186`, `:203`, `:214`,
  `fleet_upgrade_test.go:295`, `:407`, `:482`) and 2 in `wait.PollUntilContextTimeout`
  (`tls_test.go:817`, `:853`). `pod_security_test.go:203` and `:214` go through `authTLSExec`
  ([`pod_security_test.go:84-88`](../../test/e2e/pod_security_test.go)), a wrapper that calls
  `valkeyTLSExec` with `-a <password> --no-auth-warning` prepended; `fleet_upgrade_test.go` goes
  through the wrapper `m.exec` (`:143-149`). Ten of the 20 send `INFO`, which carries the
  `loading` command flag (measured with `COMMAND INFO`), so `LOADING` never reaches them; the
  other ten send `GET`, `DBSIZE` or `LLEN`, which do not carry it. *(Corrected at `84a39c2`: T12
  named "at least ten"; the first scripted count for this file found 18 because it did not follow
  the `authTLSExec` wrapper; the adversarial review of 2026-09-27 recounted 20. No other wrapper
  of a strict helper exists in `test/e2e`: a scan for functions that call `valkeyExec`,
  `valkeyTLSExec` or `m.exec` finds `valkeyMSET`, `authTLSExec`, `fleetMember.exec`,
  `waitForFleetReplicas`, `fleetMSET` and the test body `runNoSecondDeleteScenario`, and no manual
  `time.Sleep` loop around a strict call.)*

**Verified 2026-09-27 at `84a39c2`:** the helper code and its retry arithmetic; the 15 discarding
write sites and their read-backs; the two discarding `SENTINEL FAILOVER` calls and their replies
with and without `-e` (both pins, Measurements); the 20 poll sites and their commands (review
recount); the three false comments (read, the first and the third against the measurement); that no
strict-helper caller branches on or asserts an error text (`grep` for
`READONLY`, `NOAUTH`, `ERR`, `LOADING`, `NOREPLICAS`, `OOM`, `!= "OK"` over `test/e2e/*.go`: every
hit is on a lenient helper or on a phase string); the `valkey-cli` behaviour with and without `-e`
for `NOREPLICAS`, `READONLY`, `OOM`, `LOADING` and `WRONGTYPE` and for the success replies, on
`valkey/valkey:9.1.1` and `8.1.9` (Measurements); the rejection sites in `processCommand` and the
`-e` branch in `valkey-cli.c` at both tags; `kubectl exec` turning a remote non-zero exit into
`command terminated with exit code <n>` with that exit status
([client-go remotecommand/v4.go at v1.36.1, lines 97-106](https://github.com/kubernetes/kubernetes/blob/v1.36.1/staging/src/k8s.io/client-go/tools/remotecommand/v4.go#L97-L106);
[kubectl cmd/util/helpers.go at v1.36.1, lines 173-174](https://github.com/kubernetes/kubernetes/blob/v1.36.1/staging/src/k8s.io/kubectl/pkg/cmd/util/helpers.go#L173-L174)),
read in source.

**Not verified:**

- Anything on Kubernetes. That `kubectl exec` forwards the remote stderr of `valkey-cli -e` and
  exits 1 is read in client-go and kubectl source, not run; no Kind cluster was used.
- Whether any CI or local e2e run has ever hit a refused write at one of the 15 sites. No run log
  was searched.
- The second failover a retried `SENTINEL FAILOVER` starts, on a three-pod cluster in Kind: it
  was measured with one master and one replica in one container, not at the two e2e call sites,
  and where a second failover leaves the master among three pods was not measured.
- The upstream line numbers (`valkey-cli.c`, `server.c`, `sentinel.c`) were read by the filing
  run; the adversarial review re-confirmed the `-e` branch text in `valkey-cli.c` 9.1.1 but not
  the line numbers, and re-measured the behaviour instead.
- `LOADING` on `INFO`: the flag is measured, the reply during a load was not (in the load
  measurement `INFO` was answered only after the load ended, see Measurements).
- The one-site difference between the two write-site totals.

### Measurements 2026-09-27 at `84a39c2`

Every run used `docker run --rm` on the local `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`,
containers named `vko-file-068-*` (the adversarial review: `vko-file-068r-*`, `vko-file-068s-*`,
`vko-file-068f-*`), all removed afterwards; no network was created. The scripts
lived in the session scratchpad and are not kept; each method is described so it can be rebuilt.
The T12 audit measured the `NOREPLICAS`, `TTL`, `GET`, `WRONGTYPE` and `PUBLISH` rows on both pins
the same day with `vko-verify-012-*` containers; the rows below re-ran them and added `READONLY`,
`OOM`, `LOADING`, the connection refusal and the command flags.

| What | Method | Result (identical on 9.1.1 and 8.1.9 unless noted) |
|---|---|---|
| `NOREPLICAS` | `valkey-server --min-replicas-to-write 1 --min-replicas-max-lag 10`, no replica; `docker exec <c> valkey-cli [-e] [--raw] SET k v`, rc, stdout and stderr captured separately | without `-e`, with and without `--raw`: rc 0, stdout `NOREPLICAS Not enough good replicas to write.` (no `-`), stderr empty. With `-e`: rc 1, stdout empty, stderr `NOREPLICAS Not enough good replicas to write.` Re-run by the adversarial review (`--min-replicas-to-write 1`, `--raw`, plus `-e --raw TTL nokey`): the same, and `TTL` rc 0 with stdout `-2` |
| `READONLY` | `valkey-server --replicaof 127.0.0.1 1` (a replica of nothing); `valkey-cli [-e] --raw SET k v` | without `-e`: rc 0, stdout `READONLY You can't write against a read only replica.`; with `-e`: rc 1, the same text on stderr |
| `OOM` | `valkey-server --maxmemory 1 --maxmemory-policy noeviction`; `valkey-cli [-e] --raw SET k v` | without `-e`: rc 0, stdout `OOM command not allowed when used memory > 'maxmemory'.`; with `-e`: rc 1, the text on stderr |
| `WRONGTYPE` | `LPUSH k x` on a string key | without `-e`: rc 0, text on stdout; with `-e`: rc 1, text on stderr |
| Success replies under `-e` | `valkey-cli -e --raw` with `SET k v`, `TTL k`, `TTL nokey`, `GET nokey`, `PING`, `PUBLISH c x`, `RPUSH l a b c`, `MSET a 1 b 2` | all rc 0; stdout `OK`, `-1`, `-2`, empty, `PONG`, `0`, `3`, `OK` |
| Connection refused | `valkey-cli [-e] --raw -p 1 PING` | rc 1 **with and without `-e`**, stderr `Could not connect to Valkey at 127.0.0.1:1: Connection refused`. So the retry loop already covers a Valkey that is not listening yet, today |
| `LOADING` | one container, `sh` script: `valkey-server --enable-debug-command yes --daemonize yes`, `DEBUG POPULATE 200000`, `SAVE`, `SHUTDOWN NOSAVE`, restart with `--key-load-delay 50`, then `valkey-cli [-e] --raw` with `GET key:1`, `DBSIZE`, `PING`, `SET x y`, `INFO persistence` | `GET` during the load: without `-e` rc 0, stdout `LOADING Valkey is loading the dataset in memory`; with `-e` rc 1, the text on stderr. The next commands were answered only after the load ended (`DBSIZE` `200000`, rc 0), because under `key-load-delay` the server serves clients only between load batches; so only `GET` was measured during the load |
| `-e` in the help | `valkey-cli --help`, `--version` | line 35: `-e                 Return exit error code when command execution fails.` on both |
| Command flags | `valkey-cli --raw COMMAND INFO <cmd>` | `info`: `loading stale`; `get`, `dbsize`, `llen`, `hget`, `scard`, `zcard`: `readonly fast`, no `loading`; `ping`: `fast` |
| Write sites | a Python script over `test/e2e/*.go`: calls of the six helpers whose balanced argument list carries a write-verb literal or ends in `...`; a call whose line starts with `tc.` counted as discarding | 64 hits in 17 files, four not call-site writes (Fact), 60 left; 27 statement-position calls, of which 12 are `valkeyMSET` (asserts `OK` inside) and 15 are the discarding strict-helper writes listed above |
| Poll sites | the same script, strict-helper calls (and the fleet `m.exec` wrapper) lexically inside the argument list of `require.Eventually`, `pollUntil` or `wait.PollUntilContextTimeout` | 18; the script did not follow `authTLSExec` |
| Poll sites, recount (adversarial review) | a Python script over `test/e2e/*.go`: for every `require.Eventually`, `assert.Eventually`, `pollUntil` and `wait.Poll*` call, the balanced argument list scanned for `tc.valkeyExec(`, `tc.valkeyTLSExec(` and `m.exec(`; then every function that calls a strict helper listed, and `authTLSExec` added by hand; `grep -B8 time.Sleep` for manual loops | 18 by the scan plus `pod_security_test.go:203`, `:214` through `authTLSExec` = 20; no manual sleep loop around a strict call |
| `SENTINEL FAILOVER` replies (adversarial review) | one container per pin: `valkey-server --port 6390`, `sentinel monitor m 127.0.0.1 6390 1`, no replica; `valkey-cli [-e] --raw -p 26379 SENTINEL FAILOVER m` | without `-e`: rc 0, stdout `NOGOODSLAVE No suitable replica to promote`; with `-e`: rc 1, the text on stderr |
| `INPROG` and a repeated failover (adversarial review) | one container per pin: master on 6390, replica on 6391, one Sentinel (`quorum 1`, `down-after-milliseconds 2000`, `failover-timeout 10000`); `valkey-cli -e --raw -p 26379 SENTINEL FAILOVER m` twice in a row, 12 s wait, `SENTINEL get-master-addr-by-name m`, the failover once more, 12 s, the address again | first call `OK` rc 0; second `INPROG Failover already in progress` rc 1; master `127.0.0.1 6391`; the third call `OK` rc 0 and the master back on `127.0.0.1 6390`. A failover is accepted again as soon as the previous one has ended |

## Impact

- **A refused write is silent at the write and misdiagnosed at the read-back.** No test passes
  because of it, since all 15 discarding writes are read back, but the failure names the wrong
  cause. The clearest cases have no asserted write before them: the rotation canary
  (`tls_rotation_test.go:131`) fails after the certificate roll as "the canary written before the
  rotation must still be there" (`:221-222`), the drain key (`sidecar_test.go:481`) as data lost
  across the replica deletion (`:499-500`), and the fleet's TLS `MSET` records a `DBSIZE` of 0
  before the upgrade (`fleet_upgrade_test.go:302`, not compared with the key count), passes the
  `DBSIZE` comparison after it, and fails at the `EXISTS` check as "should still hold key", which
  reads as data lost by the operator upgrade (`fleet_upgrade_test.go:397-416`). At `tls_test.go:830`,
  `:836` and `:842` the read-back follows on the same pod at once and fails as a bare count or
  value mismatch. At `tls_test.go:496-499`, `:1284-1298` and `rolling_update_test.go:291` an
  asserted write on the same pod comes first (`tls_test.go:488-489` and `:1278-1279`,
  `assert.Equal` on `OK`; `valkeyMSET` at `rolling_update_test.go:280`, `require.Equal` on `OK`
  at `e2e_test.go:281`), so a refusal already standing when the step starts fails there with its
  text; only a refusal that begins between the two writes is misdiagnosed there, as "replica did
  not replicate" or a list lost across the rolling update. *(Refined by the adversarial review of
  2026-09-27: the filing named `tls_test.go:1284-1298` and `rolling_update_test.go:291` as the
  examples without the asserted write in front of them.)* The triage then starts at the operator,
  where there is nothing to find.
- **When:** a write lands on a pod that is not the writable master at that moment (a replica, a
  master being demoted, a pod loading its dataset), or on a master that refuses it
  (`maxmemory`, and `NOREPLICAS` once write fencing exists). The e2e suite writes right after
  failovers and rolls, which is when these windows open.
- **It blocks write fencing.** [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md)
  option 1 (`min-replicas-to-write`) refuses writes by design in some failover windows; its e2e
  needs refused writes to be errors, so that option depends on this ticket.
- Nothing outside `test/e2e` is affected: the operator's own Valkey client returns an error reply
  as a Go error (`ExecMulti`, [`client.go:333-358`](../../internal/valkeyclient/client.go), read by
  the T12 audit at `84a39c2`; the `-` branch of `readFullResponse` returns
  `valkey error: <text>`, [`client.go:479-481`](../../internal/valkeyclient/client.go), re-read by
  the adversarial review).

## Options

### D1 - how `valkey-cli -e` reaches the two strict helpers

**What the code does today.** `valkeyExec` and `valkeyTLSExec` treat every non-zero exit of
`kubectl exec` as a transient transport failure, retry it (five attempts and 28 s of sleep, or
three and 4 s) and then fail with "kubectl exec failed". A Valkey error reply never produces a
non-zero exit, so it never reaches that path. `-e` changes exactly that: an error reply becomes
exit 1, which `kubectl exec` passes through as exit 1 with the reply text on stderr (read in
source). The choice is what the helper does with that exit: treat it like any other non-zero exit
(retry, then fail), or tell it apart and fail at once.

**What the choice does not change.** The lenient helpers (`valkeyExecAllowError`,
`valkeyTLSExecAllowError`, `valkeyExecQuick`) stay without `-e`: their callers read the error
text from stdout or rely on `""`. The pre-existing retry of a write whose `kubectl exec` broke
after the server executed it (a non-idempotent `RPUSH` sent twice) stays as it is under every
option. The 15 read-backs stay. The two `SENTINEL FAILOVER` calls leave the strict helpers under
A and B alike (Work list): a retried `SENTINEL FAILOVER` starts a second failover once the first
has ended (measured), so it must not ride any retry path that fires after a reply.

- **B - add `-e` to both strict helpers and nothing else but the log line (recommended).** XS:
  `"-e",` next to `"--raw",` at `e2e_test.go:247` and `tls_test.go:63`, and the last error added to
  the retry log lines (`e2e_test.go:238`, `tls_test.go:54`) so a retried refusal is named in the
  run log. An error reply then takes the existing retry path. A refusal that clears within it
  (`LOADING` on a replica loading a small dataset, `READONLY` just before a role flip) heals and
  the write lands; that is safe because the four refusals are rejected before execution (Fact). A
  refusal that persists fails the test with the reply text in the message, because `lastErr`
  carries the stderr (`e2e_test.go:263`, `tls_test.go:83`), for example
  `kubectl exec failed for pod x-1: exit status 1 (stderr: READONLY You can't write against a read
  only replica. command terminated with exit code 1)` (shape read from the code and client-go, not
  run). No call site and no wait is touched, so ADR 0017 D25 (every touched wait moves from
  `require.Eventually` to `wait.PollUntilContextTimeout`) is not triggered. Consequences: the label
  "kubectl exec failed" is wrong for a reply error, the text next to it is right; a persistent
  refusal costs 28 s (4 s over TLS) before the test fails, on the failure path only; inside the 20
  polls an error reply is retried in the helper instead of ending the tick, and one that outlasts
  the retries now ends the test from inside the poll instead of at the poll's budget. Ten of the
  20 send `INFO`, which `LOADING` never reaches (measured flag). The 11 `require.Eventually` sites
  then call `require.NoError` from the condition goroutine, which the helper already does today
  for a transport failure (ADR 0017 D25's hazard, not new in kind). Every error reply of a strict
  call is sent again, not only the four refusals: that is harmless for every command the strict
  helpers send today (`GET`, `MGET`, `SET`, `MSET`, `RPUSH`, `HSET`, `SADD`, `ZADD` and their
  reads, `INFO`, `DBSIZE`, `EXISTS`, `PING`, `CONFIG GET`, `SENTINEL master`, `BGSAVE`,
  `BGREWRITEAOF`; a script over every call of the strict helpers and their wrappers, argument
  lists followed across lines, by the adversarial review) except `SENTINEL FAILOVER`, which
  therefore moves out (Work list item 4); a future strict call whose repetition after an error
  reply has an effect of its own carries the same hazard under B.
- **A - `-e` plus a classifier and a poll-safe variant.** S: before the retry loop, an exit whose
  stderr carries kubectl's `command terminated with exit code 1` and not `valkey-cli`'s
  `Could not connect to Valkey` is a Valkey reply error and fails at once, labelled as one; a new
  variant returns the reply and the error without failing (with a counterpart for `authTLSExec`),
  and the 20 poll sites move to it, which under ADR 0017 D25 converts the 11 `require.Eventually`
  sites among them to `wait.PollUntilContextTimeout`. Consequences: the fastest failure with the correct label; no
  refusal is healed by a retry, so a transient `LOADING` on a strict read outside a poll fails at
  once (today the following assertion fails on the text, so the outcome is the same); the
  classifier rests on two message texts that this repository does not control (kubectl's and
  `valkey-cli`'s); about 20 helper lines, and 20 call sites in five files plus the helpers in
  `e2e_test.go` and `tls_test.go`, all in files carrying `//go:build e2e` that `make lint` and
  `make vet` do not see
  ([T43](043-lint-and-vet-skip-every-build-tagged-test-file.md)), so the review surface is checked
  only by the e2e compile and run.
- **D - assert the reply at the 15 discarding sites, no helper change.** S: `require.Equal` on
  `OK` or on the integer each write returns. Consequences: fixes today's 15 sites and leaves the
  helper contract as it is, so the next write that discards its reply repeats the defect, and
  every strict call that asserts a value other than the reply (the reads inside the 20 polls, for
  one) still takes an error text for an answer. It fixes the symptom at the call site where B and
  A fix the helper.

**Why B.** It closes the defect completely: after it, no error reply can pass a strict helper as
a success, at every current and future call site, with two argv lines and two log lines. A's
advantages over B are diagnostics on the failure path (the label, and 28 s sooner), and it pays for
them with a classifier bound to two foreign message texts, a second helper (and a second
`authTLSExec`), and 20 call-site moves with 11 wait conversions in files that lint does not check;
a passing suite sees none of that difference. B's healing of a transient refusal is the right
behaviour for these callers: none of them tests a refusal (every refusal test uses a lenient
helper, Fact), so a write that lands after one retry is the state the test wanted, and the retry
log line records that it happened.

The strongest case for A, argued: A never sends a command again after Valkey has answered it, so
the class of hazard the `SENTINEL FAILOVER` measurement exposed cannot arise under A, now or for a
future call; under B it is closed per command, by moving the two calls out and by the rule that a
command whose repetition has an effect does not go through a strict helper. That is a real
difference in kind, and it does not overturn the mark: every command the strict helpers send
today is harmless to repeat after an error reply (the list under B), the one that is not leaves
under both options, and A pays for the general guarantee with the classifier on two foreign texts
and the 20 moves, where B's residual is one rule for the author of a new call. D loses to both
because it does not change the helper. *(This corrects the T12 facts review, which held that
the helper "has to classify a `valkey-cli` error reply ... before the retry loop": the reply text
reaches the failure message without a classifier, so classifying is option A, not a
requirement.)*

## Decision

Not decided.

## Work list

1. **D1** (needs Hans): option B, A or D.
2. **Needs no decision:** `"-e",` in the argv of `valkeyExec` and `valkeyTLSExec` only, never in
   the lenient helpers (common to A and B; D does not take it).
3. **Needs no decision:** the retry log lines name the last error (`e2e_test.go:238`,
   `tls_test.go:54`); useful under every option, because today a retried transport failure is
   logged without its cause.
4. **Common to A and B:** the two `SENTINEL FAILOVER` calls
   (`sentinel_stale_master_test.go:80`, `sentinel_peer_table_test.go:92`) go through
   `valkeyExecAllowError`, which has no `-e`, retries only on a kubectl transport error
   (`standalone_test.go:584-589`) and returns the first reply that reaches it, and assert it:
   `OK`, or a reply starting with `INPROG` (a failover already running serves both tests, which
   check the outcome in the wait that follows). Under B without this item an `INPROG` is retried
   and can start a second failover; under A without it an `INPROG` fails the test at once where
   today the following wait passes. D leaves the calls as they are.
5. **Needs no decision, rule 1:** correct the three comments (Mechanism):
   `standalone_test.go:591` (a `READONLY` reply exits 0 and leaves through the success return),
   `e2e_test.go:228` ("up to 5 times with exponential backoff" becomes five attempts with a linear
   4, 6, 8, 10 s delay) and `e2e_test.go:520-522` (`""` on an exec error; a Valkey error reply
   comes back as its text). Independent of D1 and can land first.
6. Under A only: the classifier, the poll-safe variant and its `authTLSExec` counterpart, the 20
   poll sites and their D25 conversions.
7. **Coordinate with [T34](034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md):**
   both edit `test/e2e/e2e_test.go`, in different functions (here `valkeyExec`, `:225-268`; T34's
   recommended option E the readiness waits `waitForStatefulSetReady`, `:147`, and
   `waitForPodReady`, `:285`). The edits only need to be merged; both need the full e2e suite on
   both Valkey lines, and one run can serve both.
8. ~~T12 names this move only in words ("moves to a ticket of its own") and does not link this
   file; the link belongs in T12 (its section "E2E impact" and its prerequisite 4) in the next
   edit of T12.~~ *(Done 2026-09-27 by T12's host update; checked in the sweep.)* On done: T12's
   pointer records that the prerequisite of T12's option 1 has landed.

## Verification

- **Revert check (ADR 0017 D7), on a local Kind cluster:** point one discarding write at a replica
  for the check only (for example `sidecar_test.go:481` at a replica pod). With the fix, the test
  fails at the write with `READONLY` in the message; with `-e` removed, it fails later at the
  read-back (`sidecar_test.go:499-500`) with a value mismatch. Both failure messages are recorded
  in the change, then the site is restored and `git diff` shows only the fix.
- `grep -n '"-e",' test/e2e/e2e_test.go test/e2e/tls_test.go test/e2e/standalone_test.go` shows the
  flag in `valkeyExec` and `valkeyTLSExec` and nowhere in the lenient helpers.
- The full e2e suite green on both Valkey lines (the run can be T34's), and the log grepped for
  `Retrying valkeyExec` / `Retrying valkeyTLSExec` lines that carry a reply text; each is a refusal
  the suite used to swallow and is recorded in History.
- `grep -n 'SENTINEL", "FAILOVER' test/e2e/*.go` shows both calls on `valkeyExecAllowError`
  with an assertion on the reply.
- The three comments corrected: `grep -n 'exponential\|e.g., Valkey READONLY\|on any error'
  test/e2e/e2e_test.go test/e2e/standalone_test.go` finds none of the false wordings.
- Under A additionally: the 20 poll sites use the variant, and none of the touched waits is a
  `require.Eventually` (D25).

## Related tickets

- [T12](012-no-write-fencing-min-replicas-to-write-as-an-opt-in-field.md): the host. Its option 1
  (write fencing) needs this ticket landed first; its sections "E2E impact" and the Work list item
  "The e2e reply check" moved here.
- [T34](034-e2e-fixtures-wait-on-controller-state-after-a-pod-delete.md): same file, different
  functions, one full run (Work list item 7). The two compose: a fixture of T34's class that
  writes into a pod on its way to becoming a replica would, after this fix, fail at the write with
  `READONLY` instead of at a later read (read, not measured).
- [T43](043-lint-and-vet-skip-every-build-tagged-test-file.md): the edited files carry
  `//go:build e2e`, which lint and vet skip.

## History

- 2026-09-27: filed from T12 (section "E2E impact", Work list item "The e2e reply check", the
  `valkey-cli` row of its Measurements, and the audit, facts and design results of its
  re-verification) during the re-verification at `84a39c2`. **Moved:** the mechanism, the 15
  discarding sites, the write-site count of 61 with its method, the Eventually sites, the retry
  arithmetic (4, 6, 8, 10 s), the refutation of the `-` prefix rule and the `-e` measurement.
  **Re-verified now:** every helper location and the retry code; the 15 sites site by site and, new,
  that each is read back later, so no test passes because of the defect and the harm is the wrong
  diagnosis; that no strict-helper caller relies on an error text; the rejection sites in
  `processCommand` and the `-e` branch in `valkey-cli.c` at tags 9.1.1 and 8.1.9; kubectl's exit
  code propagation in client-go and kubectl source at v1.36.1. **Measured** (docker, both pins,
  Measurements): `NOREPLICAS`, `READONLY`, `OOM`, `LOADING` and `WRONGTYPE` with and without `-e`,
  the success replies under `-e`, the connection refusal (exit 1 with and without `-e`), the `-e`
  help line, and the `loading` command flag. **Corrected:** the poll sites are ~~18 (11
  `require.Eventually`, 5 `pollUntil`, 2 `wait.PollUntilContextTimeout`)~~ 20 (11
  `require.Eventually`, 7 `pollUntil`, 2 `wait.PollUntilContextTimeout`; corrected by the
  adversarial review below), not "at least ten"; the
  write-site total recounts to 60 with a different script (the 15 discarding sites agree); T12's
  facts review held that the helper must classify the reply before its retry loop, which is one
  option (A), not a requirement. **New:** two discarding `SENTINEL FAILOVER` calls. **Options:** B
  (plain `-e`, recommended), A (classifier and poll variant), D (per-site assertions). Security
  `none`; severity `low`; ~~urgency `later` by rule 4. Rule 1 was checked and does not match: the
  doc comments of the lenient helpers (`standalone_test.go:550`, `tls_test.go:90`) say those
  helpers "do not fail on Valkey errors", which is true of them and implies, but does not state,
  that the strict ones do; Hans may read the implication as a false statement and move the ticket
  to `now`.~~ **Adversarial review, same day:** urgency **changed from `later` to `now`, rule 1**:
  the comment at `standalone_test.go:591` states that a `READONLY` reply makes `kubectl exec` exit
  non-zero, which the measurement refutes; `e2e_test.go:228` ("exponential backoff") and
  `:520-522` (`valkeyExecQuick` "returns \"\" on any error") are false as well; their correction
  is Work list item 5. **Corrected:** the poll sites are 20, not 18 (`authTLSExec` at
  `pod_security_test.go:203`, `:214` was not followed); T12 does not yet link this file (the
  opening paragraph said it keeps a pointer); the Impact examples (at `tls_test.go:1284-1298` and
  `rolling_update_test.go:291` an asserted write on the same pod comes first, so the clearest
  misdiagnoses are the rotation canary, the drain key and the fleet `MSET`). **Measured** (docker,
  both pins): `NOREPLICAS` and `TTL nokey` under `-e` again; `SENTINEL FAILOVER` answering
  `NOGOODSLAVE` (rc 0 without, rc 1 with `-e`) and `INPROG` (rc 1 with `-e`); a repeated `SENTINEL FAILOVER` accepted once the running failover has ended, starting a
  second failover. **New:** that hazard, and with it Work list item 4 (both `SENTINEL FAILOVER`
  calls leave the strict helpers under A and B); option B now names that every strict error reply
  is retried, not only the four refusals, and why that is harmless for today's commands; the case
  for A argued against B, the mark kept. Security `none` confirmed (test code, no operator
  guarantee rests on it), so the file name without `local_` is right; severity `low` and effort
  `S` kept.
  Sweep: T12 now links this file (E2E impact, prerequisite 4, Work list, Related tickets), so the
  opening paragraph's "does not link this file yet" and Work list item 8's pending link are struck
  as done; what remains of item 8 is the pointer update in T12 when this ticket is done. Frontmatter
  unchanged.
