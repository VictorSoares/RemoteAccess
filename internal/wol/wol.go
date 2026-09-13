package wol

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
)

// GetPrimaryMACAddress detects the MAC address of the active physical network interface
func GetPrimaryMACAddress() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		mac := iface.HardwareAddr.String()
		if len(iface.HardwareAddr) == 6 && mac != "" {
			return strings.ToUpper(mac)
		}
	}
	return ""
}

// SendMagicPacket constructs and broadcasts a Wake-on-LAN Magic Packet to wake up a remote machine
func SendMagicPacket(macStr string) error {
	cleanMAC := strings.ReplaceAll(macStr, ":", "")
	cleanMAC = strings.ReplaceAll(cleanMAC, "-", "")
	cleanMAC = strings.ReplaceAll(cleanMAC, ".", "")
	cleanMAC = strings.TrimSpace(cleanMAC)

	if len(cleanMAC) != 12 {
		return fmt.Errorf("MAC address invalido (necessario 12 digitos hex): %s", macStr)
	}

	macBytes, err := hex.DecodeString(cleanMAC)
	if err != nil || len(macBytes) != 6 {
		return fmt.Errorf("falha ao decodificar MAC address: %w", err)
	}

	// 102-byte Magic Packet: 6 bytes of 0xFF + 16 repetitions of 6-byte target MAC
	var packet bytes.Buffer
	packet.Write(bytes.Repeat([]byte{0xFF}, 6))
	for i := 0; i < 16; i++ {
		packet.Write(macBytes)
	}

	broadcastAddrs := []string{
		"255.255.255.255:9",
		"255.255.255.255:7",
	}

	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
				ip := ipNet.IP.To4()
				mask := ipNet.Mask
				if len(mask) == 4 {
					bcast := make(net.IP, 4)
					for i := 0; i < 4; i++ {
						bcast[i] = ip[i] | ^mask[i]
					}
					broadcastAddrs = append(broadcastAddrs, fmt.Sprintf("%s:9", bcast.String()))
				}
			}
		}
	}

	var lastErr error
	for _, addrStr := range broadcastAddrs {
		addr, err := net.ResolveUDPAddr("udp4", addrStr)
		if err != nil {
			continue
		}
		conn, err := net.DialUDP("udp4", nil, addr)
		if err != nil {
			lastErr = err
			continue
		}
		_, err = conn.Write(packet.Bytes())
		conn.Close()
		if err != nil {
			lastErr = err
		}
	}

	return lastErr
}
