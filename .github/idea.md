My ideas:

- The operator must be able to update clusters that were created by older versions of the operator itself and bring them to the target state of its current version. This means the operator must be able to update resources created by an older version so that they become compatible with the current version.

- Which permissions do the sidecars of the Valkey instances have on a Kubernetes cluster?

- After the password Secret is updated, I want the Valkey instances to switch to the new password automatically, without losing their state, even when they run without a PV.

- The user must be able to control whether the PVs are kept or deleted when the Valkey cluster is deleted.
