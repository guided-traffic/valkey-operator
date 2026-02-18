package valkeyclient

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- formatRESP ---

func TestFormatRESP_SingleArg(t *testing.T) {
	resp := formatRESP([]string{"PING"})
	assert.Equal(t, "*1\r\n$4\r\nPING\r\n", resp)
}

func TestFormatRESP_MultipleArgs(t *testing.T) {
	resp := formatRESP([]string{"INFO", "replication"})
	assert.Equal(t, "*2\r\n$4\r\nINFO\r\n$11\r\nreplication\r\n", resp)
}

func TestFormatRESP_ThreeArgs(t *testing.T) {
	resp := formatRESP([]string{"SENTINEL", "MASTER", "test"})
	assert.Equal(t, "*3\r\n$8\r\nSENTINEL\r\n$6\r\nMASTER\r\n$4\r\ntest\r\n", resp)
}

// --- parseReplicationInfo ---

func TestParseReplicationInfo_Master(t *testing.T) {
	raw := `# Replication
role:master
connected_slaves:2
slave0:ip=10.0.0.2,port=6379,state=online
slave1:ip=10.0.0.3,port=6379,state=online
master_replid:abc123
master_sync_in_progress:0
`
	info := parseReplicationInfo(raw)

	assert.Equal(t, "master", info.Role)
	assert.Equal(t, 2, info.ConnectedSlaves)
	assert.False(t, info.MasterSyncInProgress)
}

func TestParseReplicationInfo_Replica(t *testing.T) {
	raw := `# Replication
role:slave
master_host:test-0.test-headless.default.svc.cluster.local
master_port:6379
master_link_status:up
master_sync_in_progress:0
`
	info := parseReplicationInfo(raw)

	assert.Equal(t, "slave", info.Role)
	assert.Equal(t, "test-0.test-headless.default.svc.cluster.local", info.MasterHost)
	assert.Equal(t, "6379", info.MasterPort)
	assert.Equal(t, "up", info.MasterLinkStatus)
	assert.False(t, info.MasterSyncInProgress)
}

func TestParseReplicationInfo_SyncInProgress(t *testing.T) {
	raw := `role:slave
master_host:10.0.0.1
master_port:6379
master_link_status:up
master_sync_in_progress:1
`
	info := parseReplicationInfo(raw)

	assert.True(t, info.MasterSyncInProgress)
}

func TestParseReplicationInfo_EmptyInput(t *testing.T) {
	info := parseReplicationInfo("")
	assert.Equal(t, "", info.Role)
	assert.Equal(t, 0, info.ConnectedSlaves)
}

// --- parseSentinelMasterInfo ---

func TestParseSentinelMasterInfo(t *testing.T) {
	raw := `name
test
ip
test-0.test-headless.default.svc.cluster.local
port
6379
flags
master
num-slaves
2
quorum
2
`
	info := parseSentinelMasterInfo(raw)

	assert.Equal(t, "test", info.Name)
	assert.Equal(t, "test-0.test-headless.default.svc.cluster.local", info.IP)
	assert.Equal(t, "6379", info.Port)
	assert.Equal(t, "master", info.Flags)
	assert.Equal(t, 2, info.NumSlaves)
	assert.Equal(t, 2, info.Quorum)
}

func TestParseSentinelMasterInfo_EmptyInput(t *testing.T) {
	info := parseSentinelMasterInfo("")
	assert.Equal(t, "", info.Name)
}

// --- New ---

func TestNewClient(t *testing.T) {
	c := New("localhost:6379")
	assert.Equal(t, "localhost:6379", c.addr)
}

func TestFormatRESP_WaitCommand(t *testing.T) {
	resp := formatRESP([]string{"WAIT", "2", "5000"})
	assert.Equal(t, "*3\r\n$4\r\nWAIT\r\n$1\r\n2\r\n$4\r\n5000\r\n", resp)
}
