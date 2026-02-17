// Package valkeyclient provides a lightweight Valkey/Redis RESP protocol client
// for health checking and replication monitoring.
package valkeyclient

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"
)

// Client is a minimal Valkey/Redis client using raw RESP protocol.
// It does not depend on any external Redis Go client library.
type Client struct {
	addr    string
	timeout time.Duration
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

// exec sends a RESP command and reads the response.
func (c *Client) exec(args ...string) (string, error) {
	conn, err := net.DialTimeout("tcp", c.addr, c.timeout)
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
	var result strings.Builder

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

	case strings.HasPrefix(line, "$"):
		// Bulk string.
		size := 0
		if _, err := fmt.Sscanf(line[1:], "%d", &size); err != nil {
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

	case strings.HasPrefix(line, "*"):
		// Array — read all elements.
		count := 0
		if _, err := fmt.Sscanf(line[1:], "%d", &count); err != nil {
			return "", fmt.Errorf("parsing array count: %w", err)
		}
		for i := 0; i < count; i++ {
			elemLine, err := reader.ReadString('\n')
			if err != nil {
				return "", err
			}
			elemLine = strings.TrimRight(elemLine, "\r\n")

			if strings.HasPrefix(elemLine, "$") {
				size := 0
				if _, err := fmt.Sscanf(elemLine[1:], "%d", &size); err != nil {
					return "", fmt.Errorf("parsing array element size: %w", err)
				}
				if size < 0 {
					result.WriteString("(nil)\n")
					continue
				}
				buf := make([]byte, size+2)
				n := 0
				for n < len(buf) {
					read, err := reader.Read(buf[n:])
					if err != nil {
						return "", err
					}
					n += read
				}
				result.WriteString(string(buf[:size]))
				result.WriteString("\n")
			}
		}
		return result.String(), nil

	default:
		return line, nil
	}
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
