//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestDropResolvedSyncRetries pins the one thing the filter must not do: hide a
// "Connection refused" that no successful sync followed. Needs no cluster.
func TestDropResolvedSyncRetries(t *testing.T) {
	const refusal = "1:S 22 Aug 2026 17:17:38.126 # Error condition on socket for SYNC: Connection refused"
	const finished9 = "1:S 22 Aug 2026 17:17:45.226 * PRIMARY <-> REPLICA sync: Finished with success"
	const finished8 = "1:S 22 Aug 2026 17:17:45.226 * MASTER <-> REPLICA sync: Finished with success"

	tests := []struct {
		name string
		logs []string
		kept bool
	}{
		{
			name: "retry before a Valkey 9 sync is dropped",
			logs: []string{refusal, finished9},
			kept: false,
		},
		{
			name: "retry before a Valkey 8 sync is dropped",
			logs: []string{refusal, finished8},
			kept: false,
		},
		{
			// The exact ordering that turned the single-node leg red: one refusal
			// while pod-0 was still binding its port, then the sync that followed.
			name: "the startup ordering that flaked in CI is dropped",
			logs: []string{
				"1:S 22 Aug 2026 17:17:38.124 * Connecting to PRIMARY tls-ha-nosent-0.tls-ha-nosent-headless.e2e-tls-ha-nosent.svc.cluster.local:16379",
				"1:S 22 Aug 2026 17:17:38.126 * PRIMARY <-> REPLICA sync started",
				refusal,
				"1:S 22 Aug 2026 17:17:39.140 * Connecting to PRIMARY tls-ha-nosent-0.tls-ha-nosent-headless.e2e-tls-ha-nosent.svc.cluster.local:16379",
				"1:S 22 Aug 2026 17:17:44.799 * Full resync from primary: 966e601c2a3cf54a16e562bb22e80461d6f3ff30:0",
				finished9,
			},
			kept: false,
		},
		{
			name: "refusal after the last sync is kept",
			logs: []string{refusal, finished9, refusal},
			kept: true,
		},
		{
			name: "refusal with no sync at all is kept",
			logs: []string{refusal, refusal},
			kept: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dropResolvedSyncRetries(strings.Join(tt.logs, "\n"))
			if has := strings.Contains(got, "Connection refused"); has != tt.kept {
				t.Fatalf("Connection refused kept = %v, want %v; filtered log:\n%s", has, tt.kept, got)
			}
		})
	}
}
