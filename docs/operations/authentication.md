# Authentication

What `spec.auth` turns on, which processes use the password, and how to change the password
of a running cluster. The fields — `spec.auth.secretName`, `spec.auth.secretPasswordKey` and
`spec.sentinel.disableAuth` — are in the [CRD reference](../../README.md#crd-reference); a
complete manifest is [HA with authentication](examples.md#ha--with-authentication). Where the
password lives and who can read it is
[the cluster password](../security/secrets-and-tls.md#the-cluster-password). The decision is
[ADR 0016](../adr/0016-authentication-and-tls-posture.md).

## What `spec.auth` sets

Authentication is on only when `spec.auth.secretName` is set. It names a Secret you create and
own; the operator never generates one
([ADR 0016](../adr/0016-authentication-and-tls-posture.md) D1). A `Valkey` without `spec.auth`
runs an unauthenticated Valkey, and the network is the only barrier.

One password serves every purpose:

- Every data pod starts `valkey-server` with it as `requirepass` **and** as `masterauth`, so a
  replica authenticates to its master with the password the clients use.
- On a Sentinel cluster, the Sentinel init container writes it into `sentinel.conf` when the
  pod starts: as `sentinel auth-pass`, with which Sentinel talks to the data pods, and as
  Sentinel's own `requirepass` unless `spec.sentinel.disableAuth` is set
  ([example](examples.md#ha--with-authentication-sentinel-unauthenticated)).
- The sidecar, the metrics exporter and the observer authenticate with it, and so does the
  operator.

Which container receives it under which environment variable is in the
[naming conventions](../../README.md#naming-conventions).

## Changing the password

**Changing the password inside the Secret restarts nothing.** Every pod read the password
when it started, and keeps using the old one. The operator reads the Secret
each time it connects and uses the new password from its next pass. Why the change does not
propagate is [the password rotation gap](../security/rotation-and-change-propagation.md#the-password-rotation-gap).
Automatic propagation is not implemented, so the pods are replaced by hand, and the cluster is
degraded until the last one is: plan the change as a maintenance window.

### What happens until every pod is replaced

Read from the code, not measured on a cluster:

- **The operator's health checks fail until the roll is done.** From its next pass the
  operator authenticates with the new password, against every pod that still runs with the
  old one.
- **A replaced replica cannot sync from the old master.** `masterauth` is the same value as
  `requirepass`, so a replica started with the new password is refused by a master still
  running with the old one, until the master is replaced as well. Until then it serves what
  it loaded from its own volume when it started. Its readiness probe authenticates with the
  pod's own password, so the replacement still becomes Ready.
- **Without persistence the dataset is lost.** A single pod without persistence loses its
  data when it is replaced. A cluster of more pods without persistence loses it as well: the
  dataset exists only in the memory of the pods still running with the old password, and by
  the previous point no pod started with the new one can receive it by replication.
- **Without Sentinel, a replaced pod-0 that is not the master can start as a second one.**
  Its init container looks for the master with the new password, gets no answer from the
  master still running with the old one, and falls back to the ordinal rule, under which pod-0
  starts as master. Its sidecar then labels it `master`, which puts it into `<name>-rw` next
  to the real master. When pod-0 is the master, this does not arise.

### Steps

**The order of these steps is not verified.** No e2e test changes the password of a running
cluster, and no measured run of this procedure is recorded; the order and the reasons given
for it are read from the code.

1. **Change the Secret** — the key `spec.auth.secretPasswordKey` names, in the Secret
   `spec.auth.secretName` names. If the Secret is generated elsewhere (GitOps, an external
   secret store), change it there. By hand:

   ```bash
   # example; "password" stands for the key spec.auth.secretPasswordKey names
   kubectl create secret generic <secret> --from-literal=password="$NEW_PASSWORD" \
     --dry-run=client -o yaml | kubectl apply -f -
   ```

2. **Replace the data pods, replicas first, master last.** The master is the data pod its
   sidecar labelled `master`:

   ```bash
   kubectl get pods -l vko.gtrfc.com/cluster=<name>,vko.gtrfc.com/instanceRole=master
   ```

   Delete one replica at a time (`kubectl delete pod <name>-<ordinal>`) and wait until its
   replacement is Ready before deleting the next. Then delete the master. Deleting it starts
   the hand-over every master delete starts: on a Sentinel cluster its sidecar asks Sentinel
   for a failover, without Sentinel the sidecar promotes a synced replica itself. No replica
   is synced now (above), so neither finds one to promote, and the master's replacement starts
   on the same ordinal with what its own volume holds. A cluster of a single data pod has no
   replica: delete that pod.

3. **On a Sentinel cluster, replace the Sentinel pods**, one at a time, once the data pods are
   done. A Sentinel writes `requirepass` and `sentinel auth-pass` into its `sentinel.conf` when
   its pod starts, so a running Sentinel keeps the old password. Why after the data pods: a
   Sentinel still on the old password cannot authenticate to a replaced replica, so it cannot
   promote one either; a Sentinel already on the new password while the master still runs with
   the old one sees the master as down and could promote a replaced replica, which holds only
   what it loaded when it started.

4. **If the observer is enabled, delete its pod.** It reads the password once at start, like
   every other pod, and its Deployment brings it back with the new one:

   ```bash
   kubectl delete pod -l app.kubernetes.io/component=observer,vko.gtrfc.com/cluster=<name>
   ```

### Pointing `spec.auth.secretName` at another Secret

The Secret's name is part of the pod spec, so changing it does roll the pods, through the
failover-aware [rolling update](rolling-updates.md). It is not a way around the steps above
when the new Secret holds a different password: read from the code and not measured, the first
replaced replica cannot sync from the master (above), and the rolling update waits for that
sync until `spec.rollingUpdate.syncTimeout` and then reports `RollingUpdatePaused`
([what `syncTimeout` bounds](rolling-updates.md#what-synctimeout-bounds)).
