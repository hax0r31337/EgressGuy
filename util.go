package main

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

const (
	ANSI_ERASE_LINE = "\x1b[2K\r"
	ANSI_RESET      = "\x1b[0m"

	ANSI_BLACK  = "\x1b[30m"
	ANSI_RED    = "\x1b[31m"
	ANSI_GREEN  = "\x1b[32m"
	ANSI_YELLOW = "\x1b[33m"
	ANSI_BLUE   = "\x1b[34m"
	ANSI_PURPLE = "\x1b[35m"
	ANSI_CYAN   = "\x1b[36m"
	ANSI_WHITE  = "\x1b[37m"
)

var GOPACKETS_OPTS = gopacket.SerializeOptions{
	FixLengths:       true,
	ComputeChecksums: true,
}

const ETHER_OFFSET = 14

func GetHwAddr(handle *pcap.Handle, iface *net.Interface, src net.IP, gw net.IP) (net.HardwareAddr, error) {
	start := time.Now()
	// Prepare the layers to send for an ARP request.
	eth := layers.Ethernet{
		SrcMAC:       iface.HardwareAddr,
		DstMAC:       net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		EthernetType: layers.EthernetTypeARP,
	}
	arp := layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   []byte(iface.HardwareAddr),
		SourceProtAddress: []byte(src),
		DstHwAddress:      []byte{0, 0, 0, 0, 0, 0},
		DstProtAddress:    []byte(gw),
	}

	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf, GOPACKETS_OPTS, &eth, &arp); err != nil {
		return nil, err
	}
	if err := handle.WritePacketData(buf.Bytes()); err != nil {
		return nil, err
	}

	// Wait 3 seconds for an ARP reply.
	for {
		if time.Since(start) > time.Second*3 {
			return nil, errors.New("timeout getting ARP reply")
		}
		data, _, err := handle.ZeroCopyReadPacketData()
		if err == pcap.NextErrorTimeoutExpired {
			continue
		} else if err != nil {
			return nil, err
		}
		packet := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.NoCopy)
		if arpLayer := packet.Layer(layers.LayerTypeARP); arpLayer != nil {
			arp := arpLayer.(*layers.ARP)
			if net.IP(arp.SourceProtAddress).Equal(net.IP(gw)) {
				return net.HardwareAddr(arp.SourceHwAddress), nil
			}
		}
	}
}

func HumanizeBytes(bytes uint64) string {
	if bytes > 1<<30 {
		return fmt.Sprintf("%.2f GiB", float64(bytes)/(1<<30))
	} else if bytes > 1<<20 {
		return fmt.Sprintf("%.2f MiB", float64(bytes)/(1<<20))
	} else if bytes > 1<<10 {
		return fmt.Sprintf("%.2f KiB", float64(bytes)/(1<<10))
	} else {
		return fmt.Sprintf("%d B", bytes)
	}
}
