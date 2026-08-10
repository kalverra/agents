package llm

import (
	"fmt"
	"net"
	"strings"
)

// SendMagicPacket constructs and sends a Wake-on-LAN magic packet to macStr.
func SendMagicPacket(macStr, broadcastAddr string) error {
	macStr = strings.ReplaceAll(macStr, "-", ":")
	hwAddr, err := net.ParseMAC(macStr)
	if err != nil {
		return fmt.Errorf("invalid MAC address %q: %w", macStr, err)
	}
	if len(hwAddr) != 6 {
		return fmt.Errorf("invalid MAC address length for %q: expected 6 bytes", macStr)
	}

	if broadcastAddr == "" {
		broadcastAddr = "255.255.255.255:9"
	}

	udpAddr, err := net.ResolveUDPAddr("udp", broadcastAddr)
	if err != nil {
		return fmt.Errorf("resolving WoL broadcast address %q: %w", broadcastAddr, err)
	}

	var packet [102]byte
	for i := range 6 {
		packet[i] = 0xFF
	}
	for i := range 16 {
		copy(packet[6+i*6:6+(i+1)*6], hwAddr)
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("dialing WoL address %q: %w", broadcastAddr, err)
	}
	defer func() { _ = conn.Close() }()

	_, err = conn.Write(packet[:])
	if err != nil {
		return fmt.Errorf("sending WoL magic packet: %w", err)
	}

	return nil
}
