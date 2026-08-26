package observer

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/guided-traffic/valkey-operator/internal/tlsmaterial"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// discoverMaster identifies the current master address.
// If Sentinel is enabled, it queries Sentinel; for multi-replica non-Sentinel
// setups, it probes all pods via INFO REPLICATION to find the actual master
// (preventing false positives after manual failover). Falls back to pod-0.
func (o *Observer) discoverMaster(_ context.Context) (string, error) {
	if o.cfg.SentinelEnabled && len(o.cfg.SentinelAddrList) > 0 {
		return o.discoverMasterViaSentinel()
	}
	if o.cfg.Replicas > 1 {
		if addr, err := o.discoverMasterViaProbe(); err == nil {
			return addr, nil
		}
	}
	return o.masterAddressFromHeadless(), nil
}

// discoverMasterViaProbe queries all pods for INFO REPLICATION and returns
// the address of the master with the most connected replicas.
// This prevents the observer from writing to a stale pod-0 after failover.
func (o *Observer) discoverMasterViaProbe() (string, error) {
	port := 6379
	if o.cfg.TLSEnabled {
		port = 16379
	}

	var bestAddr string
	bestSlaves := -1

	for i := 0; i < o.cfg.Replicas; i++ {
		addr := fmt.Sprintf("%s-%d.%s:%d", o.cfg.ClusterName, i, o.cfg.ValkeyHeadlessSvc, port)
		client := o.newClient(addr, o.cfg.Password)
		info, err := client.InfoReplication()
		if err != nil {
			continue
		}
		if info.Role == roleMaster && info.ConnectedSlaves > bestSlaves {
			bestAddr = addr
			bestSlaves = info.ConnectedSlaves
		}
	}
	if bestAddr == "" {
		return "", fmt.Errorf("no master found among %d pods", o.cfg.Replicas)
	}
	return bestAddr, nil
}

func (o *Observer) discoverMasterViaSentinel() (string, error) {
	password := o.cfg.Password
	if o.cfg.SentinelDisableAuth {
		password = ""
	}

	for _, addr := range o.cfg.SentinelAddrList {
		client := o.newClient(addr, password)
		info, err := client.SentinelMaster(o.cfg.SentinelMonitor)
		if err != nil {
			continue
		}
		return fmt.Sprintf("%s:%s", info.IP, info.Port), nil
	}
	return "", fmt.Errorf("no sentinel responded with master info for %s", o.cfg.SentinelMonitor)
}

func (o *Observer) masterAddressFromHeadless() string {
	port := 6379
	if o.cfg.TLSEnabled {
		port = 16379
	}
	// Pod-0 is the master in standalone or ordinal-based mode.
	return fmt.Sprintf("%s-0.%s:%d", o.cfg.ClusterName, o.cfg.ValkeyHeadlessSvc, port)
}

// pingHost sends a PING command to the given address.
func (o *Observer) pingHost(addr string) error {
	client := o.newClient(addr, o.cfg.Password)
	return client.Ping()
}

// checkReplicaSync verifies that all replicas are connected and synced by querying INFO REPLICATION on the master.
func (o *Observer) checkReplicaSync(masterAddr string) error {
	client := o.newClient(masterAddr, o.cfg.Password)
	info, err := client.InfoReplication()
	if err != nil {
		return fmt.Errorf("INFO REPLICATION: %w", err)
	}

	expectedReplicas := o.cfg.Replicas - 1
	if info.ConnectedSlaves < expectedReplicas {
		return fmt.Errorf("expected %d connected replicas, got %d", expectedReplicas, info.ConnectedSlaves)
	}
	if info.MasterSyncInProgress {
		return fmt.Errorf("master sync in progress")
	}
	return nil
}

// writeHealthKey writes the observer health key to the master using SELECT + SET with TTL.
func (o *Observer) writeHealthKey(masterAddr, value string) error {
	client := o.newClient(masterAddr, o.cfg.Password)
	return client.ExecMulti(
		[]string{selectCommand, fmt.Sprintf("%d", o.cfg.ObserverDB)},
		[]string{"SET", observerHealthKey, value, "EX", "10"},
	)
}

// readHealthKey reads the observer health key and verifies the expected value.
func (o *Observer) readHealthKey(addr, expectedValue string) error {
	client := o.newClient(addr, o.cfg.Password)
	val, err := client.ExecGet(
		[]string{selectCommand, fmt.Sprintf("%d", o.cfg.ObserverDB)},
		[]string{"GET", observerHealthKey},
	)
	if err != nil {
		return fmt.Errorf("GET health key: %w", err)
	}
	if val != expectedValue {
		return fmt.Errorf("expected %q, got %q", expectedValue, val)
	}
	return nil
}

// checkReplicaRead verifies that each replica can read the health key.
func (o *Observer) checkReplicaRead(expectedValue string) error {
	port := 6379
	if o.cfg.TLSEnabled {
		port = 16379
	}
	for i := 0; i < o.cfg.Replicas; i++ {
		addr := fmt.Sprintf("%s-%d.%s:%d", o.cfg.ClusterName, i, o.cfg.ValkeyHeadlessSvc, port)
		// Skip master (will be the pod that returned master info).
		// Check all pods since we cannot easily distinguish.
		client := o.newClient(addr, o.cfg.Password)
		val, err := client.ExecGet(
			[]string{"SELECT", fmt.Sprintf("%d", o.cfg.ObserverDB)},
			[]string{"GET", "__vko_observer_health"},
		)
		if err != nil {
			return fmt.Errorf("replica %s-%d: GET: %w", o.cfg.ClusterName, i, err)
		}
		if val != expectedValue {
			return fmt.Errorf("replica %s-%d returned stale data: expected %q, got %q",
				o.cfg.ClusterName, i, expectedValue, val)
		}
	}
	return nil
}

// checkSentinelReachable pings all configured sentinels.
func (o *Observer) checkSentinelReachable() error {
	password := o.cfg.Password
	if o.cfg.SentinelDisableAuth {
		password = ""
	}

	var errs []string
	for _, addr := range o.cfg.SentinelAddrList {
		client := o.newSentinelClient(addr, password)
		if err := client.Ping(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", addr, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("unreachable sentinels: %s", strings.Join(errs, "; "))
	}
	return nil
}

// checkSentinelQuorumAndFlags queries all sentinels for master info and checks
// quorum consistency and master flags.
func (o *Observer) checkSentinelQuorumAndFlags() (quorumOK, flagsOK bool, err error) {
	password := o.cfg.Password
	if o.cfg.SentinelDisableAuth {
		password = ""
	}

	quorumOK = true
	flagsOK = true

	masterIPs := make([]string, 0, len(o.cfg.SentinelAddrList))
	for _, addr := range o.cfg.SentinelAddrList {
		client := o.newSentinelClient(addr, password)
		info, cErr := client.SentinelMaster(o.cfg.SentinelMonitor)
		if cErr != nil {
			quorumOK = false
			err = fmt.Errorf("sentinel %s: %w", addr, cErr)
			continue
		}
		masterIPs = append(masterIPs, fmt.Sprintf("%s:%s", info.IP, info.Port))

		// Check flags for s_down or o_down.
		if strings.Contains(info.Flags, "s_down") || strings.Contains(info.Flags, "o_down") {
			flagsOK = false
			err = fmt.Errorf("sentinel %s reports master flags: %s", addr, info.Flags)
		}
	}

	// Check quorum: all sentinels must agree on the same master.
	if len(masterIPs) > 1 {
		for i := 1; i < len(masterIPs); i++ {
			if masterIPs[i] != masterIPs[0] {
				quorumOK = false
				err = fmt.Errorf("sentinel quorum inconsistent: %v", masterIPs)
				break
			}
		}
	}

	return quorumOK, flagsOK, err
}

// newClient creates a valkeyclient.Client with appropriate TLS/auth config.
//
// The TLS config is taken from the reloader per call, never stored: the client
// holds no connection, so the next command handshakes with whatever material is
// on disk at that moment.
func (o *Observer) newClient(addr, password string) *valkeyclient.Client {
	return newTLSAwareClient(addr, o.tlsSrc, password)
}

// newSentinelClient creates a valkeyclient.Client for Sentinel connections.
// When TLS is enabled it uses the Sentinel material source (CA-only unless
// sentinel mTLS was opted into), falling back to the Valkey one if no
// sentinel-specific source was built.
func (o *Observer) newSentinelClient(addr, password string) *valkeyclient.Client {
	src := o.sentinelTLSSrc
	if src == nil {
		src = o.tlsSrc
	}
	return newTLSAwareClient(addr, src, password)
}

// newTLSAwareClient builds a client for addr, reading the current TLS material
// from src. A nil src means TLS is disabled.
func newTLSAwareClient(addr string, src *tlsmaterial.Reloader, password string) *valkeyclient.Client {
	switch {
	case src != nil && password != "":
		return valkeyclient.NewTLSWithPassword(addr, src.Config(), password)
	case src != nil:
		return valkeyclient.NewTLS(addr, src.Config())
	case password != "":
		return valkeyclient.NewWithPassword(addr, password)
	default:
		return valkeyclient.New(addr)
	}
}

// checkSentinelMasterHostname queries all Sentinels and verifies the master address
// is a hostname rather than a raw IP address.
func (o *Observer) checkSentinelMasterHostname() error {
	password := o.cfg.Password
	if o.cfg.SentinelDisableAuth {
		password = ""
	}

	var errs []string
	for _, addr := range o.cfg.SentinelAddrList {
		client := o.newSentinelClient(addr, password)
		info, err := client.SentinelMaster(o.cfg.SentinelMonitor)
		if err != nil {
			errs = append(errs, fmt.Sprintf("sentinel %s: %v", addr, err))
			continue
		}
		if net.ParseIP(info.IP) != nil {
			errs = append(errs, fmt.Sprintf("sentinel %s returned IP %s instead of hostname for master", addr, info.IP))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("master hostname check failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

// checkSentinelReplicaHostnames queries all Sentinels and verifies every replica
// address is a hostname rather than a raw IP address.
func (o *Observer) checkSentinelReplicaHostnames() error {
	password := o.cfg.Password
	if o.cfg.SentinelDisableAuth {
		password = ""
	}

	var errs []string
	for _, addr := range o.cfg.SentinelAddrList {
		client := o.newSentinelClient(addr, password)
		replicas, err := client.SentinelReplicas(o.cfg.SentinelMonitor)
		if err != nil {
			errs = append(errs, fmt.Sprintf("sentinel %s: %v", addr, err))
			continue
		}
		for _, r := range replicas {
			if net.ParseIP(r.IP) != nil {
				errs = append(errs, fmt.Sprintf("sentinel %s returned IP %s instead of hostname for replica", addr, r.IP))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("replica hostname check failed: %s", strings.Join(errs, "; "))
	}
	return nil
}
