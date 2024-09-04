package egressguy

import (
	"log"
	"net"
	"sync"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
)

type EgressGuy struct {
	Src           net.IP
	ethernetLayer []byte

	Interface    *net.Interface
	Handle       *pcap.Handle
	Listeners    []PacketListener
	listenerLock sync.Mutex

	Traffic uint64
}

func NewEgressGuy(iface *net.Interface, src net.IP, gw net.IP) (*EgressGuy, error) {
	handle, err := pcap.OpenLive(iface.Name, int32(iface.MTU), true, pcap.BlockForever)
	if err != nil {
		return nil, err
	}

	hwAddr, err := GetHwAddr(handle, iface, src, gw)
	if err != nil {
		return nil, err
	}

	handle.SetBPFFilter("tcp")
	handle.SetDirection(pcap.DirectionIn)

	eth := layers.Ethernet{
		SrcMAC:       iface.HardwareAddr,
		DstMAC:       hwAddr,
		EthernetType: layers.EthernetTypeIPv4,
	}
	buf := gopacket.NewSerializeBuffer()
	if err := eth.SerializeTo(buf, GOPACKETS_OPTS); err != nil {
		return nil, err
	}
	b := make([]byte, 14)
	copy(b, buf.Bytes())

	return &EgressGuy{
		Src:           src,
		ethernetLayer: b,
		Interface:     iface,
		Handle:        handle,
		Listeners:     make([]PacketListener, 0),
	}, nil
}

func (eg *EgressGuy) Close() {
	eg.Handle.Close()
}

func (eg *EgressGuy) SendPacket(layersArr ...gopacket.SerializableLayer) error {
	buf := gopacket.NewSerializeBuffer()

	if err := gopacket.SerializeLayers(buf, GOPACKETS_OPTS, layersArr...); err != nil {
		return err
	}

	if b, err := buf.PrependBytes(14); err != nil {
		return err
	} else {
		copy(b, eg.ethernetLayer)
	}
	buf.PushLayer(layers.LayerTypeEthernet)

	if err := eg.Handle.WritePacketData(buf.Bytes()); err != nil {
		return err
	}

	return nil
}

func (eg *EgressGuy) ListenPackets() {
	decoder := eg.Handle.LinkType()

	for {
		d, ci, err := eg.Handle.ZeroCopyReadPacketData()
		if err != nil {
			log.Println(err)
			return
		}

		if d[12] != 0x08 || d[13] != 0x00 {
			continue
		}

		packet := gopacket.NewPacket(d, decoder, gopacket.NoCopy)
		ip4 := packet.Layer(layers.LayerTypeIPv4).(*layers.IPv4)

		// eg.listenerLock.Lock()
		for _, listener := range eg.Listeners {
			proto, src, dst, srcPort, dstPort := listener.ConnectionTuple()
			if proto != ip4.Protocol || !src.Equal(ip4.DstIP) || !dst.Equal(ip4.SrcIP) {
				continue
			}

			if proto == layers.IPProtocolTCP {
				if layerTCP := packet.Layer(layers.LayerTypeTCP); layerTCP != nil {
					tcp := layerTCP.(*layers.TCP)
					if srcPort == tcp.DstPort && dstPort == tcp.SrcPort {
						listener.HandlePacket(packet, layerTCP)

						eg.Traffic += uint64(ci.Length)
					}
				}
			}
		}
		// eg.listenerLock.Unlock()
	}
}

func (eg *EgressGuy) AddListener(listener PacketListener) {
	eg.listenerLock.Lock()
	defer eg.listenerLock.Unlock()

	eg.Listeners = append(eg.Listeners, listener)
}

func (eg *EgressGuy) RemoveListener(listener PacketListener) {
	eg.listenerLock.Lock()
	defer eg.listenerLock.Unlock()

	for i, l := range eg.Listeners {
		if l == listener {
			eg.Listeners = append(eg.Listeners[:i], eg.Listeners[i+1:]...)
			break
		}
	}
}

type PacketListener interface {
	ConnectionTuple() (proto layers.IPProtocol, src, dst net.IP, srcPort, dstPort layers.TCPPort)
	HandlePacket(packet gopacket.Packet, layer gopacket.Layer)
}
