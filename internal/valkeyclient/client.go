// Package valkeyclient provides a lightweight Valkey/Redis RESP protocol client
// for health checking and replication monitoring.
package valkeyclient

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"
)

// Client is a minimal Valkey/Redis client using raw RESP protocol.
// It does not depend on any external Redis Go client library.
type Client struct {
	addr      string
	timeout   time.Duration
	tlsConfig *tls.Config
}

// ReplicationInfo holds parsed INFO replication output.
type ReplicationInfo struct {
	Role                 string
	ConnectedSlaves      int
	MasterHost           string
	MasterPort           string
	MasterLinkStatus     string
	MasterSyncInProgress bool
}

// SentinelMasterInfo holds parsed SENTINEL MASTER output.
type SentinelMasterInfo struct {
	Name      string
	IP        string
	Port      string
	NumSlaves int
	Quorum    int
	Flags     string
}

// New creates a new Valkey client for the given address (host:port).
func New(addr string) *Client {
	return &Client{
		addr:    addr,
		timeout: 5 * time.Second,
	}
}

// NewTLS creates a new Valkey client that connects using TLS.
// The tlsConfig must contain at least the CA certificate for server verification.
func NewTLS(addr string, tlsConfig *tls.Config) *Client {
	return &Client{
		addr:      addr,
		timeout:   5 * time.Second,
		tlsConfig: tlsConfig,
	}
}

// Ping sends a PING command and returns nil if the response is PONG.
func (c *Client) Ping() error {
	resp, err := c.exec("PING")
	if err != nil {
		return fmt.Errorf("ping %s: %w", c.addr, err)
	}
	if !strings.Contains(resp, "PONG") {
		return fmt.Errorf("ping %s: unexpected response: %s", c.addr, resp)
	}
	return nil
}

// InfoReplication sends INFO replication and parses the result.
func (c *Client) InfoReplication() (*ReplicationInfo, error) {
	resp, err := c.exec("INFO", "replication")
	if err != nil {
		return nil, fmt.Errorf("info replication %s: %w", c.addr, err)
	}
	return parseReplicationInfo(resp), nil
}

// SentinelMaster sends SENTINEL MASTER <name> and parses the result.
func (c *Client) SentinelMaster(name string) (*SentinelMasterInfo, error) {
	resp, err := c.exec("SENTINEL", "MASTER", name)
	if err != nil {
		return nil, fmt.Errorf("sentinel master %s on %s: %w", name, c.addr, err)
	}
	return parseSentinelMasterInfo(resp), nil
}

// SentinelFailover sends SENTINEL FAILOVER <name> to trigger a manual failover.
func (c *Client) SentinelFailover(name string) error {
	_, err := c.exec("SENTINEL", "FAILOVER", name)
	if err != nil {
		return fmt.Errorf("sentinel failover %s on %s: %w", name, c.addr, err)
	}
	return nil
}

// SentinelReset sends SENTINEL RESET <pattern> to reset all matching masters.
// This clears the sentinel's internal failover cooldown state, allowing
// a new failover to be triggered immediately. The pattern "*" matches all masters.
func (c *Client) SentinelReset(pattern string) error {
	_, err := c.exec("SENTINEL", "RESET", pattern)
	if err != nil {
		return fmt.Errorf("sentinel reset %s on %s: %w", pattern, c.addr, err)
	}
	return nil
}

// SentinelRemove sends SENTINEL REMOVE <name> to remove a monitored master
// and all its associated replicas and sentinels from sentinel's tracking.
func (c *Client) SentinelRemove(name string) error {
	_, err := c.exec("SENTINEL", "REMOVE", name)
	if err != nil {
		return fmt.Errorf("sentinel remove %s on %s: %w", name, c.addr, err)
	}
	return nil
}

// SentinelMonitorAdd sends SENTINEL MONITOR <name> <ip> <port> <quorum>
// to add a new master to sentinel monitoring.
func (c *Client) SentinelMonitorAdd(name, ip string, port, quorum int) error {
	_, err := c.exec("SENTINEL", "MONITOR", name, ip, fmt.Sprintf("%d", port), fmt.Sprintf("%d", quorum))
	if err != nil {
		return fmt.Errorf("sentinel monitor %s %s:%d on %s: %w", name, ip, port, c.addr, err)
	}
	return nil
}

// SentinelSet sends SENTINEL SET <name> <option> <value> to change a sentinel
// configuration parameter for a monitored master.
func (c *Client) SentinelSet(name, option, value string) error {
	_, err := c.exec("SENTINEL", "SET", name, option, value)
	if err != nil {
		return fmt.Errorf("sentinel set %s %s %s on %s: %w", name, option, value, c.addr, err)
	}
	return nil
}

// Wait sends the WAIT command to block until all the previous write commands
// are acknowledged by at least numReplicas replicas, or until the timeout
// (in milliseconds) expires. It returns the number of replicas that acknowledged.
func (c *Client) Wait(numReplicas int, timeoutMs int) (int, error) {
	resp, err := c.exec("WAIT", fmt.Sprintf("%d", numReplicas), fmt.Sprintf("%d", timeoutMs))
	if err != nil {
		return 0, fmt.Errorf("wait on %s: %w", c.addr, err)
	}
	var acked int
	if _, err := fmt.Sscanf(resp, "%d", &acked); err != nil {
		return 0, fmt.Errorf("parsing wait response %q on %s: %w", resp, c.addr, err)
	}
	return acked, nil
}

// DBSize sends the DBSIZE command and returns the number of keys in the current database.
func (c *Client) DBSize() (int, error) {
	resp, err := c.exec("DBSIZE")
	if err != nil {
		return 0, fmt.Errorf("dbsize on %s: %w", c.addr, err)
	}
	var size int
	if _, err := fmt.Sscanf(resp, "%d", &size); err != nil {
		return 0, fmt.Errorf("parsing dbsize response %q on %s: %w", resp, c.addr, err)
	}
	return size, nil
}

// exec sends a RESP command and reads the response.
func (c *Client) exec(args ...string) (string, error) {
	var conn net.Conn
	var err error

	if c.tlsConfig != nil {
		dialer := &net.Dialer{Timeout: c.timeout}
		conn, err = tls.DialWithDialer(dialer, "tcp", c.addr, c.tlsConfig)
	} else {
		conn, err = net.DialTimeout("tcp", c.addr, c.timeout)
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return "", err
	}

	// Send command in RESP format.
	cmd := formatRESP(args)
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return "", err
	}

	// Read full response.
	return readFullResponse(conn)
}

// formatRESP formats a command into RESP array format.
func formatRESP(args []string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*%d\r\n", len(args)))
	for _, arg := range args {
		sb.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(arg), arg))
	}
	return sb.String()
}

// readFullResponse reads the complete RESP response from the connection.
func readFullResponse(conn net.Conn) (string, error) {
	reader := bufio.NewReader(conn)

	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")

	switch {
	case strings.HasPrefix(line, "+"):
		// Simple string.
		return line[1:], nil

	case strings.HasPrefix(line, "-"):
		// Error.
		return "", fmt.Errorf("valkey error: %s", line[1:])

	case strings.HasPrefix(line, ":"):
		// Integer response.
		return line[1:], nil

	case strings.HasPrefix(line, "$"):
		// Bulk string.
		return readBulkString(reader, line)

	case strings.HasPrefix(line, "*"):
		// Array — read all elements.
		return readArray(reader, line)

	default:
		return line, nil
	}
}

// readBulkString reads a RESP bulk string from the reader.
// The header line (e.g. "$6") must be passed in.
func readBulkString(reader *bufio.Reader, header string) (string, error) {
	size := 0
	if _, err := fmt.Sscanf(header[1:], "%d", &size); err != nil {
		return "", fmt.Errorf("parsing bulk string size: %w", err)
	}
	if size < 0 {
		return "", nil
	}
	buf := make([]byte, size+2) // +2 for \r\n
	n := 0
	for n < len(buf) {
		read, err := reader.Read(buf[n:])
		if err != nil {
			return "", err
		}
		n += read
	}
	return string(buf[:size]), nil
}

// readArray reads a RESP array response from the reader.
// The header line (e.g. "*3") must be passed in.
func readArray(reader *bufio.Reader, header string) (string, error) {
	count := 0
	if _, err := fmt.Sscanf(header[1:], "%d", &count); err != nil {
		return "", fmt.Errorf("parsing array count: %w", err)
	}

	var result strings.Builder
	for i := 0; i < count; i++ {
		elemLine, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		elemLine = strings.TrimRight(elemLine, "\r\n")

		if strings.HasPrefix(elemLine, "$") {
			val, err := readBulkString(reader, elemLine)
			if err != nil {
				return "", err
			}
			if val == "" {
				result.WriteString("(nil)\n")
			} else {
				result.WriteString(val)
				result.WriteString("\n")
			}
		}
	}
	return result.String(), nil
}

// parseReplicationInfo parses the INFO replication output into structured data.
func parseReplicationInfo(raw string) *ReplicationInfo {
	info := &ReplicationInfo{}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		val := parts[1]

		switch key {
		case "role":
			info.Role = val
		case "connected_slaves":
			_, _ = fmt.Sscanf(val, "%d", &info.ConnectedSlaves)
		case "master_host":
			info.MasterHost = val
		case "master_port":
			info.MasterPort = val
		case "master_link_status":
			info.MasterLinkStatus = val
		case "master_sync_in_progress":
			info.MasterSyncInProgress = val == "1"
		}
	}
	return info
}

// parseSentinelMasterInfo parses the SENTINEL MASTER response into structured data.
// The response is an array of key-value pairs (alternating elements).
func parseSentinelMasterInfo(raw string) *SentinelMasterInfo {
	info := &SentinelMasterInfo{}
	lines := strings.Split(strings.TrimSpace(raw), "\n")

	// Parse alternating key-value pairs.
	kvMap := make(map[string]string)
	for i := 0; i+1 < len(lines); i += 2 {
		key := strings.TrimSpace(lines[i])
		val := strings.TrimSpace(lines[i+1])
		kvMap[key] = val
	}

	info.Name = kvMap["name"]
	info.IP = kvMap["ip"]
	info.Port = kvMap["port"]
	info.Flags = kvMap["flags"]
	_, _ = fmt.Sscanf(kvMap["num-slaves"], "%d", &info.NumSlaves)
	_, _ = fmt.Sscanf(kvMap["quorum"], "%d", &info.Quorum)

	return info
}
