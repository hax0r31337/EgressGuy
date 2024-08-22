package main

import (
	"crypto/rand"
	"log"
	"net"
	"sync"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

const (
	TCP_CONNECTION_NONE uint8 = iota
	TCP_CONNECTION_SYN_SENT
	TCP_CONNECTION_ESTABLISHED
	TCP_CONNECTION_FINISHED
)

type TcpConn struct {
	Instance *EgressGuy

	State uint8

	Seq uint32
	Ack uint32
	Win uint16
	Mss uint16

	IPv4Layer layers.IPv4
	SrcPort   layers.TCPPort
	DstPort   layers.TCPPort

	counter     uint8
	lastCounter uint8
	payload     []byte

	onClose     chan struct{}
	onCloseOnce sync.Once
}

func NewTcpConn(instance *EgressGuy, src, dst net.IP, srcPort, dstPort layers.TCPPort, payload []byte) (*TcpConn, error) {
	ip4 := layers.IPv4{
		SrcIP:    src,
		DstIP:    dst,
		Version:  4,
		TTL:      255,
		Protocol: layers.IPProtocolTCP,
	}

	conn := TcpConn{
		Instance:  instance,
		State:     TCP_CONNECTION_SYN_SENT,
		IPv4Layer: ip4,
		SrcPort:   srcPort,
		DstPort:   dstPort,
		Mss:       uint16(instance.Interface.MTU - 40),
		Win:       65535,
		payload:   payload,
		onClose:   make(chan struct{}),
	}

	var seq [4]byte
	rand.Read(seq[:])
	conn.Seq = uint32(seq[0])<<24 | uint32(seq[1])<<16 | uint32(seq[2])<<8 | uint32(seq[3])

	tcp := conn.NewPacket()
	tcp.SYN = true
	tcp.Options = []layers.TCPOption{
		{
			OptionType: layers.TCPOptionKindMSS,
			OptionData: []byte{byte(conn.Mss >> 8), byte(conn.Mss)},
		},
		{
			OptionType: layers.TCPOptionKindSACKPermitted,
		},
		{
			OptionType: layers.TCPOptionKindWindowScale,
			OptionData: []byte{0x09},
		},
	}

	if err := conn.SendPacket(&tcp, nil); err != nil {
		return nil, err
	}

	conn.Instance.AddListener(&conn)

	return &conn, nil
}

func (c *TcpConn) Close() error {
	return c.setClosed(true)
}

func (c *TcpConn) OnClose() <-chan struct{} {
	return c.onClose
}

func (c *TcpConn) setClosed(reset bool) error {
	c.Instance.RemoveListener(c)

	c.onCloseOnce.Do(func() {
		close(c.onClose)
	})

	if c.State == TCP_CONNECTION_FINISHED {
		return nil
	}

	if reset {
		tcp := c.NewPacket()
		tcp.RST = true

		if err := c.SendPacket(&tcp, nil); err != nil {
			return err
		}
	}

	c.State = TCP_CONNECTION_FINISHED

	return nil
}

func (c *TcpConn) NewPacket() layers.TCP {
	tcp := layers.TCP{
		SrcPort: c.SrcPort,
		DstPort: c.DstPort,
		Seq:     c.Seq,
		Ack:     c.Ack,
		Window:  c.Win,
	}
	tcp.SetNetworkLayerForChecksum(&c.IPv4Layer)
	return tcp
}

func (c *TcpConn) SendPacket(t *layers.TCP, payload []byte) error {
	if payload != nil {
		c.Seq += uint32(len(payload))
		return c.Instance.SendPacket(&c.IPv4Layer, t, gopacket.Payload(payload))
	} else {
		return c.Instance.SendPacket(&c.IPv4Layer, t)
	}
}

func (c *TcpConn) ConnectionTuple() (proto layers.IPProtocol, src, dst net.IP, srcPort, dstPort layers.TCPPort) {
	return layers.IPProtocolTCP, c.IPv4Layer.SrcIP, c.IPv4Layer.DstIP, c.SrcPort, c.DstPort
}

func (c *TcpConn) Write(payload []byte) error {
	if c.State != TCP_CONNECTION_ESTABLISHED {
		return nil
	}

	for len(payload) > 0 {
		size := int(c.Mss)
		if len(payload) < size {
			size = len(payload)
		}

		tcp := c.NewPacket()
		// although it's incorrect behavior
		// it doesn't matter in this case
		tcp.ACK = true
		tcp.PSH = true

		if err := c.SendPacket(&tcp, payload[:size]); err != nil {
			return err
		}

		payload = payload[size:]
	}

	return nil
}

func (c *TcpConn) HandlePacket(packet gopacket.Packet, layer gopacket.Layer) {
	tcp := layer.(*layers.TCP)

	switch {
	case c.State == TCP_CONNECTION_SYN_SENT && tcp.SYN && tcp.ACK:
		c.State = TCP_CONNECTION_ESTABLISHED
		c.Ack = tcp.Seq

		for _, opt := range tcp.Options {
			if opt.OptionType == layers.TCPOptionKindMSS {
				mss := uint16(opt.OptionData[0])<<8 | uint16(opt.OptionData[1])
				if mss < c.Mss {
					c.Mss = mss
				}
			}
		}

		c.Seq++
		c.Ack++

		c.Write(c.payload)
	case c.State == TCP_CONNECTION_ESTABLISHED:
		if tcp.FIN {
			c.setClosed(false)
		} else if tcp.RST {
			c.setClosed(false)
			return
		} else if len(tcp.Payload) == 0 {
			return
		} else if c.counter != 0 {
			c.counter--

			if c.Ack+uint32(len(tcp.Payload))+uint32(c.Mss) > tcp.Seq {
				return
			} else {
				c.lastCounter = 0
			}
		}
		c.counter = c.lastCounter + 1
		c.lastCounter = max(c.counter, 5)

		c.Ack = tcp.Seq

		resp := c.NewPacket()
		resp.ACK = true

		if err := c.SendPacket(&resp, nil); err != nil {
			log.Println("error sending ACK:", err)
		}
	case c.State == TCP_CONNECTION_FINISHED:
		c.Instance.RemoveListener(c)
	}
}
