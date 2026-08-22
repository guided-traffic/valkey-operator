package builder

// The operator does not build the Valkey image; it consumes the upstream one and
// runs shell in it. The container command wraps valkey-server in `sh -c` when auth
// is enabled, both init containers are shell scripts, the readiness and liveness
// probes exec valkey-cli, and the drain preStop hook is a shell loop. Every one of
// those is an assumption about a filesystem somebody else maintains.
//
// RequiredImageTools states those assumptions so they can be checked against the
// real image instead of inferred from whichever tag happened to work last
// (test/imagetools, `make test-image-tools`). The list is kept honest from the
// other side too: TestRequiredImageTools_MatchesTheGeneratedScripts walks every
// exec command the builder puts into a container that runs the Valkey image and
// fails when one of them uses a tool this list does not name.

// RequiredImageTools returns the executables the generated scripts, commands and
// probes expect to find in the Valkey image.
//
// A missing entry is not a cosmetic defect. Without `rev` and `cut` the init
// container cannot derive its own ordinal and every pod boots with the ordinal
// fallback; without `timeout` a single unreachable peer hangs the init container
// until the kubelet gives up; without `sleep` the drain preStop spins.
func RequiredImageTools() []string {
	return []string{
		// The shell every generated script runs under. POSIX features are relied on
		// beyond mere presence: $((i+1)) arithmetic in the preStop hook, `break 2`
		// out of the init container's nested discovery loops.
		"sh",

		// The Valkey binaries themselves. valkey-cli backs both probes and every
		// peer query the init containers make; valkey-sentinel is the command of the
		// Sentinel container, which runs this same image.
		"valkey-server",
		"valkey-cli",
		"valkey-sentinel",

		// Init container: peer and Sentinel discovery.
		"timeout", // bounds every valkey-cli query against a peer that may be gone
		"sleep",   // exponential backoff between discovery rounds
		"grep",    // reads role/connected_slaves out of INFO, rejects Sentinel error strings
		"cut",     // splits INFO fields and the ordinal off the hostname
		"tr",      // strips the CR that INFO replies carry
		"rev",     // reverses the hostname so the ordinal can be cut off the end
		"awk",     // reads the replicaof address out of the mounted replica config
		"head",    // takes the first line of a multi-line Sentinel reply
		"cp",      // copies the elected config into the writable mount
		"echo",    // the init containers report their decision on stdout
		"seq",     // enumerates the peer ordinals the discovery loop walks

		// Sentinel init container: rewriting the mounted config in place.
		//
		// sed is the one whose absence is silent rather than loud. It substitutes the
		// %%VALKEY_PASSWORD%% placeholder and the `sentinel monitor` line, so without
		// it Sentinel starts with the literal placeholder as its password and monitors
		// whatever the ConfigMap happened to say -- a running Sentinel that
		// authenticates against nothing, not a crash somebody notices.
		"sed",
	}
}
