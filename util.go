package egressguy

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
	"github.com/gopacket/gopacket/routing"
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
	if bytes > 1<<40 {
		return fmt.Sprintf("%.2f TiB", float64(bytes)/(1<<40))
	} else if bytes > 1<<30 {
		return fmt.Sprintf("%.2f GiB", float64(bytes)/(1<<30))
	} else if bytes > 1<<20 {
		return fmt.Sprintf("%.2f MiB", float64(bytes)/(1<<20))
	} else if bytes > 1<<10 {
		return fmt.Sprintf("%.2f KiB", float64(bytes)/(1<<10))
	} else {
		return fmt.Sprintf("%d B", bytes)
	}
}

func GetRoutingInfo(ifaceName string, dst net.IP) (*net.Interface, net.IP, net.IP, error) {
	router, err := routing.New()
	if err != nil {
		return nil, nil, nil, err
	}

	if ifaceName == "" {
		return router.Route(dst)
	} else {
		iface, err := net.InterfaceByName(ifaceName)
		if err != nil {
			return nil, nil, nil, err
		}

		iface, gw, src, err := router.RouteWithSrc(iface.HardwareAddr, nil, dst)
		if err != nil {
			return nil, nil, nil, err
		}

		if iface.Name != ifaceName {
			return nil, nil, nil, errors.New("failed to route through the specified interface")
		}

		return iface, gw, src, nil
	}
}
