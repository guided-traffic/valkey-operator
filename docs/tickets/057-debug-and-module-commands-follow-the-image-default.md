---
id: T57
title: enable-debug-command and enable-module-command follow the image default
state: analysed       # was filed; every fact the decision rests on is verified or named as not verified (History 2026-09-27)
severity: low         # off at every upstream Valkey tag from 7.2.4 through 9.1.1 and on unstable (source), measured off on both pins (docker, 2026-09-27); the gap is that nothing states or checks it
security: hardening
threat: "would additionally cover any client that reaches the data port - without a password when spec.auth is unset, with the one cluster password otherwise - running DEBUG, MODULE LOAD, or CONFIG SET on a protected config, should the valkey-server in spec.image ever default one of the three switches to yes: the generated config renders none of them, so the compiled default decides, and it is no on every upstream Valkey tag (source) and on both pins (measured)"
urgency: later        # rule 4: a cheap known fix (B, XS); rules 1-3 do not match (History 2026-09-27)
effort: XS            # for B, the recommended option; the close touches ADR 0016, ADR 0017 D53, docs/developer/testing.md and H-7, one sentence each
blocked-by: decision  # assert the default on the pins (B) or render the lines outside the config hash (A2), below; was: render the directives (A) or assert the default (B)
filed-from: T31, section "Further security measures — not in this change, each open" (archive/031)
opened: 2026-09-27
decided:              # not recorded - no decision yet
done:                 # not recorded - not done
---

Filed on 2026-09-27 from the tenth row of the table
["Further security measures — not in this change, each open"](archive/031-generated-pods-run-as-root.md#further-security-measures--not-in-this-change-each-open)
in the archived ticket 031, which is `done` and tracks none of its rows (the heading is
`archive/031:674`, verified 2026-09-27). The operator-facing statement is gap
[H-7](../security/secrets-and-tls.md#h-7). *(Added 2026-09-27.)* The scope is all three
protected-action switches — `enable-debug-command`, `enable-module-command` and
`enable-protected-configs`; the title names the first two only and is kept for continuity.

## Fact

**Verified** (read 2026-09-27; locations re-read at `84a39c2`):

- A grep for `enable-debug-command` and `enable-module-command` over `internal`, `api`, `cmd`
  and `deploy` finds nothing; the config builder
  ([`configmap.go`](../../internal/builder/configmap.go)) renders neither. *(Added 2026-09-27
  at `84a39c2`:)* `git grep -n -i 'enable-debug\|enable-module\|enable-protected' -- .
  ':!docs/tickets'` hits only [`secrets-and-tls.md:198`](../security/secrets-and-tls.md) and
  `:200` (gap H-7). The operator issues no `CONFIG SET`, `DEBUG` or `MODULE` anywhere in
  `internal/` or `cmd/`, and the CRD has no free-form config field (no `extraConfig`,
  `additionalConfig` or `customConfig` in `api/`), so a CR author cannot set these directives,
  and the `no` default costs the operator nothing.
- A data pod whose config-hash annotation differs from the desired one is outdated and is
  replaced by the rolling update
  ([`rolling_update.go:445-449`](../../internal/controller/rolling_update.go), `podOutdated`,
  which feeds `configHashFromSts` into the check at
  [`rolling_update.go:433`](../../internal/controller/rolling_update.go); the entry loop is at
  [`rolling_update.go:285-288`](../../internal/controller/rolling_update.go)), so adding a
  directive rolls every data tier on the operator upgrade that ships it. *(corrected
  2026-09-27: and every Sentinel tier. `ComputeConfigHash`
  ([`configmap.go:293-305`](../../internal/builder/configmap.go)) hashes both data configs and,
  with Sentinel, the Sentinel config into one value. That value is stamped on the data template
  ([`statefulset.go:153`](../../internal/builder/statefulset.go)) and on the Sentinel template
  ([`sentinel.go:234`](../../internal/builder/sentinel.go)), and compared by `podNeedsUpdate`
  ([`rolling_update.go:425`](../../internal/controller/rolling_update.go), through
  `podAnnotationHashChanged` at
  [`rolling_update.go:504-510`](../../internal/controller/rolling_update.go)) and by
  `sentinelPodNeedsUpdate`
  ([`rolling_update.go:4838`](../../internal/controller/rolling_update.go), check at
  [`rolling_update.go:4864-4868`](../../internal/controller/rolling_update.go)).)*
- *(Added 2026-09-27, re-read at `4a7543e`.)* The config builder is `generateValkeyConf`
  ([`configmap.go:62-138`](../../internal/builder/configmap.go)), and its `# General` block
  ([`configmap.go:116-124`](../../internal/builder/configmap.go)) is where the directives
  would go.
- *(Added 2026-09-27.)* **The default is `no` on both pinned lines, read in upstream source.**
  `src/config.c` of `valkey-io/valkey` defines `enable-debug-command` and `enable-module-command`,
  and also `enable-protected-configs`, as `IMMUTABLE_CONFIG` with default
  `PROTECTED_ACTION_ALLOWED_NO`: lines 3375-3377 at tag `9.1.1`, lines 3267-3269 at tag `8.1.9`
  (the pins at [`images.go:40, 45`](../../test/testimages/images.go)). Immutable means `CONFIG SET`
  cannot change them at runtime. The generated container runs `valkey-server <config>` directly
  ([`statefulset.go:822-833`](../../internal/builder/statefulset.go)), so the compiled default
  applies. *(Added 2026-09-27 at `84a39c2`:)* the same three lines, `IMMUTABLE_CONFIG` with
  `PROTECTED_ACTION_ALLOWED_NO`, are at lines 3140-3142 at tag `7.2.4`, the first Valkey release
  line (the earliest `valkey-io/valkey` tag is `7.2.4-rc1`, GitHub tags API), and at lines
  3547-3549 on `unstable` (fetched 2026-09-27; a moving branch). Command:
  `curl -fsSL https://raw.githubusercontent.com/valkey-io/valkey/<tag>/src/config.c | grep -n
  'createEnumConfig("enable-'`. *(Added 2026-09-27 at `84a39c2`, review:)* the same grep over
  every tag `gh api repos/valkey-io/valkey/tags --paginate` lists — 56 tags, `7.2.4-rc1` through
  `9.2.0-rc1` — and `unstable` finds all three lines as `IMMUTABLE_CONFIG` with
  `PROTECTED_ACTION_ALLOWED_NO` on every one, so "every upstream tag" below is read, not
  inferred from the endpoints. The list also shows `9.1.2` and `8.1.10`, newer than the pins;
  both carry the same default. With auth the command is
  `sh -c 'exec valkey-server <config> --requirepass "$…" --masterauth "$…"'`; neither form
  carries an `enable-*` flag.
- *(Added 2026-09-27 at `84a39c2`.)* **The pinned images were run, and they refuse all three
  protected actions.** Measured in docker on `valkey/valkey:9.1.1` and `valkey/valkey:8.1.9`, as
  uid 999 like the pod: `docker run -d --rm --name vko-verify-057w-… --user 999:999 --entrypoint
  valkey-server valkey/valkey:<tag> --save ""`, then `docker exec … valkey-cli <command>`. On both
  pins: `CONFIG GET` returns `no` for each of the three switches; `CONFIG SET
  enable-debug-command yes` (and the same for the other two) returns `ERR CONFIG SET failed
  (possibly related to argument 'enable-debug-command') - can't set immutable config`;
  `DEBUG SLEEP 0` returns `ERR DEBUG command not allowed. If the enable-debug-command option is
  set to "local", …`, from a connection inside the container as well; `MODULE LOAD /x.so` returns
  `ERR MODULE command not allowed. …`; `CONFIG SET dir /tmp` and `CONFIG SET dbfilename x.rdb`
  return `can't set protected config` (the audit also measured `CONFIG SET maxmemory-policy
  allkeys-lru` answering `OK`, so only protected configs are refused). The audit of the same day
  measured the same with a config copied from `generateValkeyConf`'s non-TLS, non-persistent
  output (`--entrypoint valkey-server … /config/valkey.conf`), and with that config plus the
  three lines set to `no`: the server boots, answers `PONG`, and `CONFIG GET` gives values
  identical to the config without them. Every container was removed afterwards
  (`docker ps -a --filter name=vko-verify-057` counts 0).
- *(Added 2026-09-27 at `84a39c2`.)* **The reply order of a multi-key `CONFIG GET` is
  unspecified.** `valkey-cli config get enable-debug-command enable-module-command
  enable-protected-configs`, three fresh containers per pin: `9.1.1` answered debug, module,
  protected each time; `8.1.9` answered protected, module, debug, then module, protected,
  debug, then debug, protected, module. Each value directly follows its key. An assertion on a
  position therefore fails at random; an assertion per key does not.
- *(Added 2026-09-27 at `84a39c2`.)* **Who can reach the commands.** Auth is opt-in: a CR
  without `spec.auth` runs an unauthenticated Valkey
  ([ADR 0016](../adr/0016-authentication-and-tls-posture.md) D1; `Auth` is `omitempty` at
  [`valkey_types.go:1041-1042`](../../api/v1/valkey_types.go), `IsAuthEnabled` at
  [`valkey_types.go:1171-1173`](../../api/v1/valkey_types.go)), and `protected-mode no` is
  always rendered ([`configmap.go:70`](../../internal/builder/configmap.go)). Measured on both
  pins without a password: `ACL LIST` answers `user default on nopass sanitize-payload ~* &*
  +@all`. Measured by the audit on `9.1.1` with `--requirepass`: `user default on
  sanitize-payload #<sha256> ~* &* +@all`, and `DEBUG` still refused. Gap
  [H-6](../security/secrets-and-tls.md#h-6) (`secrets-and-tls.md:190-194`) states that the
  components use the same full-rights password.
- *(Added 2026-09-27 at `84a39c2`.)* **The Sentinel tier is outside this gap.** Measured by the
  audit and re-measured by its fact check, on both pins: `valkey-sentinel` with a config of
  `port 26379`, `sentinel monitor m 127.0.0.1 6379 1` and the three `enable-* no` lines boots,
  answers `PONG` and logs no config error; with or without the lines, `DEBUG SLEEP 0`,
  `MODULE LIST` and `MODULE LOAD /x.so` answer `ERR unknown command` in sentinel mode.
- ~~*(Added 2026-09-27.)* **Option A reaches the single data pod of a `spec.replicas: 1` cluster
  in two ways, and one of them loses data.** `handleStandaloneRollingUpdate`
  (`rolling_update.go:3756`) asks `singlePodDeferral`
  ([`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go)).
  For a rootless pod it defers only when `isSidecarOnlyChange` (`rolling_update.go:3842-3863`,
  images only) holds. So a release that moves the sidecar image and the config hash together is
  deferred on the Helm path. Where the sidecar image does not move (kustomize or a floating tag,
  as the comment at `pod_security_migration.go:109-113` records), the pod is deleted at once
  (`rolling_update.go:3812-3813`), and a non-persistent one loses its dataset. The comment at
  lines 122-126 rests on "a configuration change is the CR author's", which A would make false.~~
  *(corrected 2026-09-27 at `84a39c2`: three ways, and two of them lose data.)* **A config-hash
  move reaches the single data pod of a `spec.replicas: 1` cluster without Sentinel in three
  ways.** `handleStandaloneRollingUpdate`
  ([`rolling_update.go:3759`](../../internal/controller/rolling_update.go), dispatched at
  [`rolling_update.go:321-328`](../../internal/controller/rolling_update.go)) asks
  `singlePodDeferral`
  ([`pod_security_migration.go:128-150`](../../internal/controller/pod_security_migration.go))
  at [`rolling_update.go:3791`](../../internal/controller/rolling_update.go):
  1. *Rootless pod, sidecar image moves* (the default Helm path: the sidecar image is
     `OPERATOR_IMAGE`, [`cmd/main.go:74`](../../cmd/main.go), rendered by the chart at
     [`deployment.yaml:48-49`](../../deploy/helm/valkey-operator/templates/deployment.yaml) from
     `repository:(image.tag or AppVersion)`,
     [`_helpers.tpl:68-77`](../../deploy/helm/valkey-operator/templates/_helpers.tpl)):
     `isSidecarOnlyChange`
     ([`rolling_update.go:3845-3866`](../../internal/controller/rolling_update.go)) compares
     images only, so the pod is deferred
     ([`pod_security_migration.go:135-139`](../../internal/controller/pod_security_migration.go)).
     The CR reports only `SidecarUpdatePending`
     ([`rolling_update.go:3828`](../../internal/controller/rolling_update.go)); nothing says the
     new configuration is held as well.
  2. *Rootless pod, sidecar image does not move* (kustomize, a floating tag or a pinned
     `image.tag`, as the comment at
     [`pod_security_migration.go:109-113`](../../internal/controller/pod_security_migration.go)
     records): the pod is deleted at once
     ([`rolling_update.go:3815-3819`](../../internal/controller/rolling_update.go),
     `deleteOwnedPod` at `:3816`), and a non-persistent one loses its dataset.
  3. *Pod still running as root, not persistent* (held under
     `PodSecurityUpdatePending=True/PodRunsAsRoot` since the rootless release): the root branch
     returns "replace" as soon as `podAnnotationHashChanged` holds
     ([`pod_security_migration.go:141-144`](../../internal/controller/pod_security_migration.go)),
     before `sidecarOnly` is consulted, so the pod is deleted with its dataset **on every install
     path, Helm included**.

  The premise that a configuration change is the CR author's and never moves on an operator
  upgrade alone is stated in the comment at
  [`pod_security_migration.go:122-127`](../../internal/controller/pod_security_migration.go),
  in [ADR 0032](../adr/0032-generated-pods-run-rootless.md) D3 (`0032:271-276`) and in the
  D6 amendment of [ADR 0007](../adr/0007-failover-aware-rolling-update.md) (`0007:260-264`,
  "None of the three moves on an operator upgrade alone"). A release that renders a new constant
  line makes that sentence false, so it reopens both. ADR 0007 D7 (`0007:273-275`) is literally
  about the pod-spec delta, and the config hash is a template annotation, so D7 is touched in
  spirit only.
- *(Added 2026-09-27.)* `spec.image` is required and chosen by the CR author
  ([`valkey_types.go:1032-1034`](../../api/v1/valkey_types.go): `MinLength=1`, json tag `image`
  without `omitempty`; the running image is `Image: v.Spec.Image` at
  [`statefulset.go:837`](../../internal/builder/statefulset.go)). The pins in `images.go` decide
  only what CI runs.
- *(Added 2026-09-27 at `84a39c2`.)* **The check that would guard the pins is a required gate.**
  The job `valkey-image-tools`, named `Valkey Image Tools`
  ([`release.yml:624-657`](../../.github/workflows/release.yml)), runs `make test-image-tools` on
  every pull request to `main`. `gh api repos/guided-traffic/valkey-operator/rules/branches/main`
  (run by the audit and by the design review, 2026-09-27) lists `Valkey Image Tools` among the
  twelve required status checks; classic branch protection answers 404 `Branch not protected`,
  so the requirement comes from a repository ruleset. Renovate caps the pins per major
  ([`renovate.json:212`](../../renovate.json) `<9`, [`renovate.json:223`](../../renovate.json)
  `<10`), so a new major arrives as a human PR, which runs the same check.
- *(Added 2026-09-27 at `84a39c2`.)* The candidate host for an assertion,
  `TestRestrictedRuntime_ValkeyServerPersistsAndAnswers`
  ([`restricted_runtime_test.go:99`](../../test/imagetools/restricted_runtime_test.go)), loops
  over `pinnedImages()` ([`image_tools_test.go:76-81`](../../test/imagetools/image_tools_test.go):
  Valkey 9 and Valkey 8) and starts `valkey-server` with command-line flags and no config file
  ([`restricted_runtime_test.go:107`](../../test/imagetools/restricted_runtime_test.go)), so a
  `CONFIG GET` there reads exactly the compiled default the generated config relies on. The test
  is ADR 0017 D53's restricted-posture test
  ([`0017-test-and-ci-policy.md:843`](../adr/0017-test-and-ci-policy.md)), and D53 does not
  mention configuration defaults.
- *(Added 2026-09-27 at `84a39c2`.)* **Release state.** `git describe --tags HEAD` gives
  `v1.13.1-3-g84a39c2`; `v1.13.1` is `7017676`. Three commits follow it: `4a7543e`, `bcc63c9`,
  `84a39c2` — docs, comments, one condition message string
  (`internal/controller/volumeclaim_conflict.go`, in `4a7543e`), one unused workflow variable
  and one Makefile help string (`bcc63c9`); none is a hash input, so none rolls anything.

**Not verified:**

- ~~The default of either directive in the two pinned images
  ([`test/testimages/images.go`](../../test/testimages/images.go)); believed `no` since
  Redis 7.~~ *(corrected 2026-09-27: verified — upstream source at both pinned tags, and at
  `84a39c2` both pinned images were run: `CONFIG GET` returns `no` for all three switches and
  every protected action is refused; see Fact. An intermediate correction of the same day still
  named the images as not run; it is recorded in History.)*
- ~~Whether Sentinel's config accepts either directive.~~ *(corrected 2026-09-27 at `84a39c2`:
  verified — `valkey-sentinel` accepts all three, and `DEBUG` and `MODULE` are unknown commands
  in sentinel mode; see Fact.)*
- ~~*(Added 2026-09-27.)* Whether any cluster runs a `spec.image` that predates these directives
  (a Redis 6 image, for example). Such a server is expected to refuse an unknown directive at
  startup, so under A every pod of such a cluster would crash-loop. Neither half was checked.~~
  *(corrected 2026-09-27 at `84a39c2`: a false premise. The container command is
  `valkey-server` ([`statefulset.go:822-833`](../../internal/builder/statefulset.go)), so an
  image without it — a Redis 6 image — cannot run under this operator at all, and every Valkey
  tag from `7.2.4` defines the three switches (Fact). `RequiredImageTools`
  ([`image_requirements.go:34-36`](../../internal/builder/image_requirements.go)) states the
  same, but it is enforced only by the imagetools tests against the pins, not at runtime.)*
- *(Added 2026-09-27 at `84a39c2`.)* Whether any production `spec.image` is a custom build whose
  compiled default differs from upstream. Not checked: this run had no cluster access. Settled
  by listing the images of every `Valkey` CR and running `CONFIG GET` on one pod per distinct
  image.
- *(Added 2026-09-27 at `84a39c2`.)* Whether any non-persistent `spec.replicas: 1` pod still runs
  as root in the fleet (path 3 in Fact). Real in code; whether its trigger is live or dormant is
  unknown for the same reason. Settled by listing `PodSecurityUpdatePending=True/PodRunsAsRoot`
  across all `Valkey` CRs.
- *(Added 2026-09-27 at `84a39c2`.)* That `+@all` includes `SHUTDOWN` and `FLUSHALL` is
  inferred from the ACL category, not measured.

## Impact

None today ~~if the believed default holds~~ *(corrected 2026-09-27: the default is verified
`no` in upstream source on both pinned lines)*. ~~The pin follows the image across Valkey majors
([ADR 0017](../adr/0017-test-and-ci-policy.md) D43), so a changed default would arrive without
a change in this repository.~~ *(corrected 2026-09-27: that conflated the test pins with
production. What runs is `spec.image`, the CR author's choice, so a changed default reaches a
cluster through its own image and never through this repository. The test pins, which Renovate
moves within a major (ADR 0017 D43), only decide what CI would notice.)* *(Added 2026-09-27 at
`84a39c2`:)* the default is `no` at every upstream tag from `7.2.4` on and measured `no` on both
pins, so the trigger — an image whose compiled default is `yes` — exists nowhere known today.

Threat, per case, should such an image ever run:

- **Principal:** any client that reaches the data port. On a cluster without `spec.auth` it needs
  no password; with auth it needs the one cluster password. Either way it already holds the
  default user with `+@all` apart from the three protected actions (Fact), so it can already
  stop or empty the server (`SHUTDOWN`, `FLUSHALL`, inferred from the category).
- **What the three add:** `DEBUG` adds introspection and odd internals — marginal next to
  `+@all`. `MODULE LOAD` is the substantive residual, and it needs a loadable object on disk:
  with the rootless posture's read-only root filesystem
  ([ADR 0032](../adr/0032-generated-pods-run-rootless.md)) and `enable-protected-configs no`
  (so `dir` and `dbfilename` stay fixed), an RDB written to `/data` is not an ELF object.
  `CONFIG SET` of a protected config is the enabler for placing such a file.
- **Sentinel pods:** out of scope, `DEBUG` and `MODULE` do not exist in sentinel mode (Fact).
- **Live or dormant:** dormant on every upstream image; not verified for a custom build (Not
  verified).

## Options

One decision.

### D1 — state the three switches in the generated config, or rely on the image default and check it on the pins

**Mechanism today.** `generateValkeyConf`
([`configmap.go:62-138`](../../internal/builder/configmap.go)) renders none of the three
switches, and the data container starts `valkey-server` with that file
([`statefulset.go:822-833`](../../internal/builder/statefulset.go)), so the compiled default of
the `valkey-server` in `spec.image` decides. That default is `no` and immutable at every
upstream tag from `7.2.4` through `9.1.1` and on `unstable`, and both pins refuse `DEBUG`,
`MODULE LOAD` and a protected `CONFIG SET` (Fact). Any line rendered into the data config enters
`ComputeConfigHash` ([`configmap.go:293-305`](../../internal/builder/configmap.go)), which is
stamped on both templates and compared on both tiers, so it rolls every data and every Sentinel
tier, and on `spec.replicas: 1` clusters without Sentinel it takes the three single-pod paths in
Fact, two of which delete a non-persistent dataset. The one existing exclusion from the hash is
`GenerateValkeyConfForHash` ([`configmap.go:50-58`](../../internal/builder/configmap.go)), which
leaves out the known-master override because that runtime state would otherwise make every pod
outdated after a failover.

**What the choice changes:** whether the operator states the value in its config (and how it
avoids paying a fleet roll for that), or relies on the image default and turns a changed default
into a red required check on the pin-bump PR. **What it does not change:** the behaviour of any
upstream Valkey image, which is `no` everywhere; who can reach the port (auth is opt-in under
ADR 0016 D1, the NetworkPolicy is optional); the Sentinel tier, where the commands do not exist;
and a CR author's custom image, which controls its own binary under every option.

- **B — render nothing, assert the compiled default on both pins, record the reliance
  (recommended).** In `TestRestrictedRuntime_ValkeyServerPersistsAndAnswers`
  ([`restricted_runtime_test.go:99`](../../test/imagetools/restricted_runtime_test.go)), or a
  small sibling function in the same package, read each switch with its own `CONFIG GET` (the
  multi-key reply order is unspecified, Fact) and assert `no`; the refusals themselves (`DEBUG`
  not allowed, `CONFIG SET dir` protected) may be asserted as well, since that is the behaviour
  that matters. The server there starts with no config file, so the test reads exactly the
  default the generated config relies on. Record in ADR 0016 that the generated config relies on
  the image default, with A2 as the revisit trigger should the check turn red. *Cost:* XS — a
  few echo and `assert.Contains` lines, the ADR 0016 sentence, an ADR 0017 D53 amendment (the
  host test is D53's restricted-posture test, so the amendment is required, not optional), the
  `docs/developer/testing.md` table row (`:19`) and "Image tools" section (`:144-157`), and the
  H-7 rewrite. *Consequences:* no roll, no ConfigMap change, on any cluster. The value stays
  implicit in the config. A changed upstream default turns the required `Valkey Image Tools`
  check red on the PR that brings it. What B leaves open is a custom-built `valkey-server` with
  a changed compile-time default, or a new Valkey major run in production before the pins
  cross — neither exists anywhere known today.
- **A2 — render the three lines as `no`, but keep them out of `ComputeConfigHash`.** The
  ConfigMaps change on the upgrade and no pod rolls. *Cost:* S — a hash-input split in
  `configmap.go` and two unit tests (the hash does not move; the ConfigMap carries the lines),
  the directive table in `docs/security/secrets-and-tls.md`, an ADR 0016 decision with a note
  against ADR 0007 D2. The ConfigMap write itself is not new: `reconcileConfigMap` already
  rewrites every ConfigMap on each operator upgrade through `OperatorVersionChanged`
  ([`valkey_controller.go:850-856`](../../internal/controller/valkey_controller.go)).
  *Consequences:* a second exclusion from the config hash, of a different kind than the
  known-master one (a constant, not runtime state), which a later edit could reuse to make a real
  config change silently skip its roll — a correctness risk to the rolling update. The lines take
  effect only when a pod is recreated: on Sentinel and multi-replica clusters an init container
  copies the config into an `emptyDir`
  ([`statefulset.go:645-647`](../../internal/builder/statefulset.go)), so a container restart
  does not pick them up; only a standalone pod, which mounts the ConfigMap directly
  ([`statefulset.go:652-657`](../../internal/builder/statefulset.go)), reads them on a container
  restart after kubelet syncs the volume. That time is unbounded — a deferred single pod can run
  for months — and in the one case where A2 adds value (an image whose default is `yes`) the
  ConfigMap says `no` while the running server still allows the commands, which misleads anyone
  who reads the ConfigMap as the effective config. And its gain is narrower than it looks:
  `spec.image` is code the CR author controls, and a build that flips a compiled default can
  just as well ignore the line, so the line adds no trust boundary; it covers only a careless
  custom build or an upstream major run ahead of the pins.

**Why B beats A2.** B closes the only realistic path for a changed default, an upstream Valkey
release, at the one point where it is cheap — the pin-bump PR, blocked by a required check
(verified in the ruleset) — and changes nothing on any cluster, so it touches none of the three
single-pod paths and reopens neither ADR 0007 nor ADR 0032. A2's extra coverage protects against
a state that exists nowhere known today (the default is `no` at `7.2.4`, `8.1.9`, `9.1.1` and on
`unstable`), takes effect only at an unbounded next recreation, and pays with a permanent second
hash exclusion whose misuse would silently skip a roll. If B's check ever turns red, A2 is the
ready next step, and the ADR 0016 record says so.

Considered and not kept (History 2026-09-27): A (render the lines inside the config hash), C (B
now, A later) and runtime detection by a status condition.

## Decision

None yet.

## Work list

1. **XS, no decision needed:** rewrite gap
   [H-7](../security/secrets-and-tls.md#h-7) (`docs/security/secrets-and-tls.md:198-203`). Its
   "Believed `no` since Redis 7; **not re-checked for either pinned Valkey line**" becomes the
   verified upstream-source default: `no`, immutable, at `9.1.1` and `8.1.9`, images not run. It
   also names `enable-protected-configs`. This is true under every option and does not close the
   ticket. **Done 2026-09-27** (History). *(Added 2026-09-27 at `84a39c2`: committed in
   `bcc63c9`, now at `secrets-and-tls.md:198-209`.)*
2. *(waits on the decision; B)* ~~Add `valkey-cli config get enable-debug-command
   enable-module-command enable-protected-configs` to the script at
   `restricted_runtime_test.go:99`, and assert `no` for each.~~ *(corrected 2026-09-27 at
   `84a39c2`: one call returns the keys in an unspecified order, Fact.)* In the script at
   [`restricted_runtime_test.go:99`](../../test/imagetools/restricted_runtime_test.go), or in a
   sibling function in `test/imagetools`, read each switch with its own call, for example
   `echo "enable-debug-command:$(valkey-cli config get enable-debug-command | tail -1)"`, and
   assert `enable-debug-command:no` with `assert.Contains`; the same for the other two.
   Optionally assert `DEBUG SLEEP 0` answers `DEBUG command not allowed` and `CONFIG SET dir`
   answers `can't set protected config`. `make test-image-tools` is green. Mutation: expect `yes`,
   and the test goes red.
3. ~~*(waits on the decision; A only)* Add three lines to the `# General` block
   (`configmap.go:116-123`), with a unit test. Revisit the premise at
   `pod_security_migration.go:122-126`. Ship only with a release that rolls the fleet anyway.~~
   *(corrected 2026-09-27 at `84a39c2`: A is no longer an option, History.)* *(waits on the
   decision; A2 only)* Add the three lines to the `# General` block
   ([`configmap.go:116-124`](../../internal/builder/configmap.go)) of the ConfigMap content and
   keep them out of the hash input next to `GenerateValkeyConfForHash`
   ([`configmap.go:50-58`](../../internal/builder/configmap.go)). Unit tests: the ConfigMap
   carries the three lines; `ComputeConfigHash` of a CR is unchanged by them; a test pins the
   exclusion to exactly these three constant lines.
4. Close ([ADR 0034](../adr/0034-tickets-are-work-lists-that-get-archived.md)): record the
   decision ~~, ADR 0016 or ADR 0033, whichever holds the Valkey config posture at the time~~
   *(corrected 2026-09-27 at `84a39c2`: in ADR 0016, which holds the Valkey server-side posture —
   `protected-mode no` at `0016:42`, `:91`, `:202`, `:284`; ADR 0033 is pod hardening)*. Under
   B, also amend ADR 0017 D53 (`0017:843`) and `docs/developer/testing.md` (the table row at
   `:19` and the "Image tools" section at `:144-157`) to say the tier checks these defaults.
   Rewrite H-7 to its final form: its heading (`secrets-and-tls.md:198`) states option A and must
   describe what was taken; drop "Not verified: the images themselves", which is now measured;
   and drop "Nothing in this repository states or checks the value", which is false once B or A2
   lands. Then `git grep` `057` and `T57`, and move to `archive/`.

Not a T57 work item: the truncated doc comment of `sentinelPodNeedsUpdate`, found in this
ticket's audit, is filed as item (c) of
[T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md) (severity low, security
hardening, effort S, state analysed, urgency now), which collects the decision-free corrections
of tracked comments, ADR sentences and pages that the 2026-09-27 re-verification found false or
stale.

## Verification

- ~~A unit test on the generated config asserts both lines. *(2026-09-27: A only.)*~~
  *(corrected 2026-09-27 at `84a39c2`: A is no longer an option.)* Under A2: a unit test asserts
  the three lines in the rendered ConfigMap content, and another that `ComputeConfigHash` does
  not move with them.
- ~~`make test-image-tools`, or an e2e step, reads `CONFIG GET enable-debug-command` and
  `CONFIG GET enable-module-command` from a running data pod on both pinned lines and gets `no`.
  *(2026-09-27: and `enable-protected-configs`. Under B, the mutation check in the work list
  applies.)*~~ *(corrected 2026-09-27 at `84a39c2`: `make test-image-tools`
  ([`Makefile:147-150`](../../Makefile)) runs docker with no cluster, and the host test starts a
  bare server, so there is no data pod in that tier.)* Under B: `make test-image-tools` reads,
  one key per call, the compiled default of all three switches from a `valkey-server` started
  with no config file on both pinned images and gets `no`; the mutation check in the work list
  applies. A read from a running data pod would be an e2e step, and neither kept option needs
  one.
- At close: `git grep -n -i 'enable-debug\|enable-module\|enable-protected'` outside
  `docs/tickets` shows the ADR 0016 record and the rewritten H-7.

## History

- 2026-09-27: re-verified at `84a39c2` (audit, fact check and design review of this run, plus own
  measurements). **Checked:** every code location (re-read at `84a39c2`; the drifted links in
  Fact were fixed in place: `rolling_update.go` 410 to 445-449 and 433, 424 to 425, 503-509 to
  504-510, 4835 and 4860-4864 to 4838 and 4864-4868, 3756 to 3759 with the call at 3791,
  3842-3863 to 3845-3866, 3812-3813 to 3815-3819; `configmap.go` 62-135 and 116-123 to 62-138
  and 116-124; `valkey_types.go` 1030-1032 to 1032-1034); upstream `config.c` at `7.2.4`
  (3140-3142) and `unstable` (3547-3549), both re-fetched with `curl` for this edit; ADR 0007
  D6/D7, ADR 0032 D3, ADR 0016 D1, ADR 0017 D53; `valkey_controller.go:848-858`;
  `statefulset.go:643-658`; the filed-from row (`archive/031:674`, tenth row). **Measured**
  (docker, both pins, uid 999, commands in Fact): `CONFIG GET` `no` for all three; `CONFIG SET`
  of each refused as immutable; `DEBUG` and `MODULE LOAD` refused; `CONFIG SET dir` and
  `dbfilename` refused as protected; `ACL LIST` `nopass … +@all` without a password; the
  multi-key `CONFIG GET` order varies between runs on `8.1.9` (three runs, three orders — the
  audit's claim that it merely differs per pin was too narrow); Sentinel accepts the lines and
  knows neither command (audit and fact check). All `vko-verify-057*` containers removed.
  **Found false or outdated:** the threat line assumed a password, but auth is opt-in (ADR 0016
  D1) and it named only two of the three actions; "the images themselves were not run" is now
  measured; the Sentinel question is answered; the Redis 6 crash-loop item rested on a false
  premise (the command is `valkey-server`); "A reaches the single pod in two ways" missed a
  third, a root non-persistent pod deleted with its data on every install path
  (`pod_security_migration.go:141-144`), which makes A contradict ADR 0032 D3 and the ADR 0007 D6
  amendment literally (D7 in spirit only); "only a docs commit follows `v1.13.1`" is three
  commits, none a hash input; work list item 2's single multi-key `CONFIG GET` would break a
  positional assertion; the Verification bullet's "from a running data pod" does not apply to
  the image-tools tier; the close's "ADR 0016 or ADR 0033" is ADR 0016, and under B the close
  also needs ADR 0017 D53, `docs/developer/testing.md:19` and `:144-157`, and the H-7 heading.
  Work list item 1 is committed in `bcc63c9`, no longer only in the working tree. T36
  (`036-non-persistent-master-restarts-empty.md:235-237`) cites "measured (docker), T57" for the
  refused `DEBUG`; that measurement now lives in this ticket's Fact. **Options:** rewritten as
  one decision. Removed: **A** (render the three lines as `no` inside the config hash) —
  disproportionate: on every upstream image it changes nothing observable (measured with and
  without the lines on both pins), it rolls every data and Sentinel tier, deletes non-persistent
  single pods on paths 2 and 3, reopens ADR 0032 D3 and the ADR 0007 D6 amendment, and the
  fleet-wide rule for a defect in the operator's own security posture does not apply because
  there is no defect; A2 gets its one gain without the roll. **C** (B now, A later on a release
  that moves every config hash) — identical to B today and its second half has no carrier: T36's
  `maxmemory` row moves only clusters with a memory limit, T12's `min-replicas-to-write` is
  opt-in, no other ticket moves every config hash; its useful part, a revisit trigger, moved into
  B's ADR 0016 record. **Runtime detection** (the operator reads the three switches from each pod
  and reports a condition), raised by the design review and never in the ticket — considered and
  not kept: a level condition with a `conditionRegistry` row (ADR 0027), unit tests and a Valkey
  round trip per pod on the health pass, to detect but not prevent a trigger that exists nowhere
  known. Added: **A2** (render the lines outside the config hash), as runner-up. The
  "Re-weighed" addendum was folded into the coherent text. **Recommendation:** unchanged, B;
  the runner-up changed from A (and C) to A2. **Cross-ticket:** T50's component ACL users would
  not restrict the client-facing default user, so T50 neither closes nor overlaps this residual;
  T41 option C (booting `valkey-server` on `GenerateValkeyConf` output per pin) would host an
  A2 assertion, and B does not depend on it; T12 and T36 are no carrier for C. **Frontmatter:**
  `state` `filed` to `analysed` (every fact the decision rests on is verified or named as not
  verified). `threat` rewritten; it was "would additionally cover an authenticated client —
  anything holding the one cluster password — running DEBUG or MODULE LOAD, should an image
  default ever enable them: today the generated config renders neither directive, so the image
  decides". Severity comment was "off on both pinned lines (upstream source, 2026-09-27); the gap
  is that nothing states or checks it"; urgency comment was "rule 4: a cheap known fix either way
  (Options); was annotated with option A's reasoning until 2026-09-27" — urgency re-derived
  top-down: rule 1 does not match (the measured-false statements are this ticket's own and are
  corrected in this edit; H-7's "not verified" is stale, not false), rule 2 nothing gates or is
  gated on a release, rule 3 severity is low, rule 4 B is a cheap known fix. `effort` gained a
  comment (XS for B, borderline because the close touches four documents). `blocked-by` comment
  was "render the directives (A) or assert the default (B), below". Values of severity,
  security, urgency, effort and blocked-by are unchanged. **Incidental, not T57 scope:** the
  truncated doc comment of `sentinelPodNeedsUpdate` (`rolling_update.go:4837`, since `73f6efe`),
  recorded under Work list; it needs its own file. **Superseded text, kept verbatim** (removed
  from Options and Not verified by this edit): the original mark read "A is marked because a
  stated value does not depend on any image default, now or after a major upgrade, and bundling
  it with a rolling release removes its only cost."; the re-weigh read "*(Re-weighed 2026-09-27;
  the mark above is superseded, not deleted.)* **B (recommended).** The default is `no` and
  immutable at runtime on both pinned lines (Fact). A buys nothing on those images, and it costs
  a roll of every data and Sentinel tier plus a data-loss path that contradicts the premise of
  `singlePodDeferral`. B is one assertion in a test that already runs, and it turns red on the
  Renovate PR that would bring a changed default. What B leaves open is a CR author's own image,
  which A would cover. The threat needs the cluster password, and that password already has
  every other right (gap H-6, T50), so this residual case is small. C is B plus a promise with no
  carrier yet." — its "the threat needs the cluster password" is the same false premise as the
  old threat line; the intermediate correction of the first Not verified item read "verified
  from upstream source at both pinned tags, see Fact. What is still not verified is the images
  themselves: neither was run with `CONFIG GET`." **Review of this edit (same day):** re-read
  every cited location at `84a39c2` and re-measured in docker on both pins (the three switches,
  their immutability, `DEBUG`, `MODULE LOAD`, protected `dir`/`dbfilename`, `ACL LIST`, and
  Sentinel with the three lines: `PONG`, `DEBUG` and `MODULE` unknown); widened the upstream
  check from four tags to all 56 tags plus `unstable` (Fact), which backs the "every upstream
  tag" wording; un-nested the first Not verified correction; fixed the H-7 heading location in
  Work list item 4 (`:198`, not `:200`); reworded the end of the `threat` line from "(measured
  on both pins)" to "(source) and on both pins (measured)". Filed: the truncated
  `sentinelPodNeedsUpdate` doc comment recorded under Work list is filed as item (c) of
  [T70](070-tracked-comments-and-adrs-state-what-the-code-contradicts.md), and the Work list
  paragraph is reduced to a pointer. T70's verification corrected this ticket's "since `73f6efe`":
  the comment was written whole in `73f6efe` (2026-03-02) and truncated in `3f0a1fe`
  (2026-03-20), `git log -S` on both lines. No frontmatter value, option or decision of this
  ticket rested on the finding, so none changes.
- 2026-09-27: work list item 1 landed, one file (read in `git diff` of the working tree):
  [`secrets-and-tls.md`](../security/secrets-and-tls.md) gap H-7 (`:198-209` now). "Believed `no`
  since Redis 7; **not re-checked for either pinned Valkey line**" is struck and corrected in
  place: on both pinned lines the default is `no`, `src/config.c` defines all three directives
  as `IMMUTABLE_CONFIG` with default `no` (lines 3375-3377 at `9.1.1`, 3267-3269 at `8.1.9`,
  re-fetched by the implementer that day), so `CONFIG SET` cannot switch them on; the builder
  renders none of the three (grep). An explicit **Not verified** names the images themselves
  (no `CONFIG GET` was run) and any other `spec.image`. The gap stays open, heading unchanged.
  The decision (B recommended) and items 2-4 are untouched; urgency `later`, severity and
  effort unchanged. **Not verified:** the upstream source was not re-read for this entry; the
  line numbers match the ones this ticket's review read the same day.
- 2026-09-27: reviewed - re-read upstream `src/config.c` at `9.1.1` and `8.1.9` (the three
  `createEnumConfig` lines hold as cited) and spot-checked the code locations; B stays
  recommended. The enrichment rewrote two frontmatter comments without keeping their old text,
  so it is recorded here verbatim: severity was `# believed off in both images; the gap is that
  nothing states it`, urgency was `# rule 4: two config lines, shipped with a release that rolls
  the data tier anyway`. Values unchanged.
- 2026-09-27: enriched - verified the default (`no`, immutable) in upstream source at both
  pinned tags. Corrected the roll scope to data and Sentinel tiers, and recorded the single-pod
  data-loss path of A. Added C, re-weighed to B (recommended), and added a work list with one
  decision-free XS item (H-7). `blocked-by: decision` added. The urgency comment now gives the
  rule-4 reason without assuming A, and the severity comment records the verified default.
  Urgency (`later`), severity (low) and effort (XS) are unchanged.
- 2026-09-27 — filed from the row "`enable-debug-command` / `enable-module-command` pinned to
  `no` in the generated config" of archive/031. Gap [H-7](../security/secrets-and-tls.md#h-7)
  states what is missing; its "open follow-up" lead-in was removed from the page in the same
  change.
