package llm_test

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/llm"
)

func TestSendMagicPacket_Success(t *testing.T) {
	t.Parallel()

	// Start a local UDP listener to capture broadcast magic packet
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	listenAddr := conn.LocalAddr().String()

	err = llm.SendMagicPacket("AA-BB-CC-DD-EE-FF", listenAddr)
	require.NoError(t, err)

	buf := make([]byte, 1024)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := conn.ReadFromUDP(buf)
	require.NoError(t, err)
	assert.Equal(t, 102, n)

	// Magic packet: 6 bytes 0xFF followed by 16 iterations of MAC (6 bytes each)
	for i := range 6 {
		assert.Equal(t, byte(0xFF), buf[i])
	}
	expectedMAC := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	for i := range 16 {
		offset := 6 + i*6
		assert.Equal(t, expectedMAC, buf[offset:offset+6])
	}
}

func TestSendMagicPacket_InvalidMAC(t *testing.T) {
	t.Parallel()

	err := llm.SendMagicPacket("invalid-mac", "127.0.0.1:9")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid MAC address")
}
