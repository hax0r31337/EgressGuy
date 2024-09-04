package egressguy

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

const TCP_WINDOW_SCALE uint8 = 9

type TcpConn struct {
	TcpHandler

	Instance *EgressGuy

	State uint8

	Seq uint32
	Ack uint32
	Win uint16
	Mss uint16

	IPv4Layer layers.IPv4
	SrcPort   layers.TCPPort
	DstPort   layers.TCPPort

	onClose     chan struct{}
	onCloseOnce sync.Once
}

func NewTcpConn(instance *EgressGuy, src, dst net.IP, srcPort, dstPort layers.TCPPort, handler TcpHandler) (*TcpConn, error) {
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
			OptionType: layers.TCPOptionKindWindowScale,
			OptionData: []byte{TCP_WINDOW_SCALE},
		},
	}

	if err := conn.SendPacket(&tcp, nil); err != nil {
		return nil, err
	}

	conn.SetHandler(handler)
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

func (c *TcpConn) SetHandler(handler TcpHandler) {
	if handler == nil {
		log.Fatal("handler cannot be nil")
	}

	if c.TcpHandler != nil {
		c.TcpHandler.SetConn(nil)
	}

	c.TcpHandler = handler
	handler.SetConn(c)
}

func (c *TcpConn) HandlePacket(packet gopacket.Packet, layer gopacket.Layer) {
	if c.TcpHandler == nil {
		log.Fatal("handler cannot be nil")
	}

	err := c.TcpHandler.HandlePacket(packet, layer.(*layers.TCP))
	if err != nil {
		log.Println("tcp handler: ", err)
	}
}
