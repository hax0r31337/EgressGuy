package main

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

type TcpHandler interface {
	SetConn(conn *TcpConn)
	HandlePacket(packet gopacket.Packet, tcp *layers.TCP) error
}

// tcp writer with reliable transmission
// resends data until acked
// but kept as simple as possible, no congestion control
type ReliableWriterHandler struct {
	conn *TcpConn

	buf     []byte
	bufLock sync.Mutex

	serverSeq         uint32
	serverWindowScale uint32
	serverWindow      uint32
	lastWrite         time.Time
}

func NewReliableWriterHandler() *ReliableWriterHandler {
	return &ReliableWriterHandler{
		buf: make([]byte, 0),
	}
}

func (c *ReliableWriterHandler) SetConn(conn *TcpConn) {
	c.conn = conn

	c.serverWindowScale = 1
	c.serverSeq = conn.Seq

	go func() {
		t := time.NewTicker(time.Second)

		for range t.C {
			if c.conn == nil || c.conn.State == TCP_CONNECTION_FINISHED {
				break
			}

			c.writeCheck()
		}
	}()
}

func (c *ReliableWriterHandler) writeCheck() bool {
	if len(c.buf) == 0 || c.conn == nil {
		return false
	}

	writeAhead := c.conn.Seq - c.serverSeq
	maxWrite := c.serverSeq + min(c.serverWindow, uint32(len(c.buf)))
	now := time.Now()
	if now.Sub(c.lastWrite) > time.Second {
		// resend
		c.conn.Seq = c.serverSeq
	} else if writeAhead >= c.serverWindow {
		return false
	} else if writeAhead >= uint32(len(c.buf)) {
		return false
	}

	if c.conn.Seq < c.serverSeq {
		c.conn.Seq = c.serverSeq
	}

	c.bufLock.Lock()
	for maxWrite > c.conn.Seq {
		off := c.conn.Seq - c.serverSeq
		d := c.buf[off:min(int(off)+int(c.conn.Mss), len(c.buf))]

		tcp := c.conn.NewPacket()
		// although it's incorrect behavior
		// it doesn't matter in this case
		tcp.ACK = true
		tcp.PSH = true

		if err := c.conn.SendPacket(&tcp, d); err != nil {
			c.bufLock.Unlock()
			return false
		}
	}
	c.lastWrite = now
	c.bufLock.Unlock()

	return true
}

func (c *ReliableWriterHandler) Write(payload []byte) (int, error) {
	if c.conn != nil && c.conn.State == TCP_CONNECTION_FINISHED {
		return 0, net.ErrClosed
	}

	c.bufLock.Lock()
	c.buf = append(c.buf, payload...)
	c.bufLock.Unlock()

	c.writeCheck()

	return len(payload), nil
}

func (c *ReliableWriterHandler) HandlePacket(packet gopacket.Packet, tcp *layers.TCP) error {
	if tcp.ACK {
		if c.serverSeq < tcp.Ack {
			// advance buffer
			c.bufLock.Lock()
			c.buf = c.buf[tcp.Ack-c.serverSeq:]
			c.bufLock.Unlock()

			c.serverSeq = tcp.Ack
		}

		c.serverWindow = uint32(tcp.Window) * c.serverWindowScale
		w := c.writeCheck()

		if tcp.SYN {
			for _, opt := range tcp.Options {
				if opt.OptionType == layers.TCPOptionKindWindowScale {
					c.serverWindowScale = 1 << uint32(opt.OptionData[0])
				}
			}

			if !w {
				// send ack
				ack := c.conn.NewPacket()
				ack.ACK = true

				if err := c.conn.SendPacket(&ack, nil); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (c *ReliableWriterHandler) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}

	return nil
}

// sends some data, then acks incoming data
// suitable for protocols without handshake, such as HTTP
type AckHandler struct {
	*ReliableWriterHandler

	counter     uint8
	lastCounter uint8
	cachedAck   layers.TCP
}

func NewAckHandler() *AckHandler {
	h := AckHandler{
		ReliableWriterHandler: NewReliableWriterHandler(),
	}

	// synack increase seq by 1
	h.Write([]byte{0xff})

	return &h
}

func NewAckHandlerWithReliableWriterHandler(h *ReliableWriterHandler) *AckHandler {
	return &AckHandler{
		ReliableWriterHandler: h,
	}
}

func (c *AckHandler) SetConn(conn *TcpConn) {
	c.ReliableWriterHandler.SetConn(conn)

	c.cachedAck = conn.NewPacket()
	c.cachedAck.ACK = true
}

func (c *AckHandler) HandlePacket(packet gopacket.Packet, tcp *layers.TCP) error {
	switch {
	case c.conn.State == TCP_CONNECTION_ESTABLISHED:
		c.ReliableWriterHandler.HandlePacket(packet, tcp)

		if tcp.FIN {
			c.conn.setClosed(false)
		} else if tcp.RST {
			c.conn.setClosed(false)
			return nil
		} else if len(tcp.Payload) == 0 {
			return nil
		} else if c.counter != 0 {
			c.counter--

			if c.conn.Ack+uint32(len(tcp.Payload))+uint32(c.conn.Mss)*2 > tcp.Seq {
				return nil
			} else {
				c.lastCounter = 1
			}
		}
		c.counter = c.lastCounter + 1
		c.lastCounter = max(c.counter, 5)

		c.conn.Ack = tcp.Seq

		c.cachedAck.Seq = c.conn.Seq
		c.cachedAck.Ack = tcp.Seq

		if err := c.conn.SendPacket(&c.cachedAck, nil); err != nil {
			return err
		}
	case c.conn.State == TCP_CONNECTION_SYN_SENT:
		if tcp.RST || tcp.FIN {
			c.conn.setClosed(false)
			return nil
		} else if !tcp.SYN || !tcp.ACK {
			return nil
		}

		c.conn.State = TCP_CONNECTION_ESTABLISHED
		c.conn.Ack = tcp.Seq

		for _, opt := range tcp.Options {
			if opt.OptionType == layers.TCPOptionKindMSS {
				mss := uint16(opt.OptionData[0])<<8 | uint16(opt.OptionData[1])
				if mss < c.conn.Mss {
					c.conn.Mss = mss
				}
			}
		}

		c.conn.Seq++
		c.conn.Ack++

		c.ReliableWriterHandler.HandlePacket(packet, tcp)
	case c.conn.State == TCP_CONNECTION_FINISHED:
		c.conn.Instance.RemoveListener(c.conn)
	}

	return nil
}

type ReliableReaderHandler struct {
	*ReliableWriterHandler

	windowSize       uint16
	windowSizeScaled uint32
	offset           uint32

	buf          []byte
	bufLock      sync.Mutex
	bufFulfilled uint

	recv chan struct{}
}

func NewReliableReaderHandler() *ReliableReaderHandler {
	h := NewReliableWriterHandler()

	h.Write([]byte{0xff})

	return NewReliableReaderHandlerWithReliableWriterHandler(h)
}

func NewReliableReaderHandlerWithReliableWriterHandler(h *ReliableWriterHandler) *ReliableReaderHandler {
	scale := uint32(1) << TCP_WINDOW_SCALE
	w := (0xffff / scale) * scale

	return &ReliableReaderHandler{
		ReliableWriterHandler: h,
		windowSize:            uint16(w / scale),
		windowSizeScaled:      w,
		buf:                   make([]byte, 0, w<<1),
		recv:                  make(chan struct{}),
	}
}

func (c *ReliableReaderHandler) SetConn(conn *TcpConn) {
	c.ReliableWriterHandler.SetConn(conn)

	c.offset = conn.Ack
}

func (c *ReliableReaderHandler) HandlePacket(packet gopacket.Packet, tcp *layers.TCP) error {
	switch {
	case c.conn.State == TCP_CONNECTION_SYN_SENT:
		if tcp.RST || tcp.FIN {
			c.conn.setClosed(false)
			return nil
		} else if !tcp.SYN || !tcp.ACK {
			return nil
		}

		c.conn.State = TCP_CONNECTION_ESTABLISHED
		c.conn.Ack = tcp.Seq

		for _, opt := range tcp.Options {
			if opt.OptionType == layers.TCPOptionKindMSS {
				mss := uint16(opt.OptionData[0])<<8 | uint16(opt.OptionData[1])
				if mss < c.conn.Mss {
					c.conn.Mss = mss
				}
			}
		}

		c.conn.Seq++
		c.conn.Ack++
		c.conn.Win = c.windowSize

		// although it's not a common case
		// for SYNACK packet to have payload
		// but it's possible

		c.offset = c.conn.Ack

		fallthrough
	case c.conn.State == TCP_CONNECTION_ESTABLISHED:
		c.ReliableWriterHandler.HandlePacket(packet, tcp)

		if tcp.FIN {
			c.conn.setClosed(false)
		} else if tcp.RST {
			c.conn.setClosed(false)
			return nil
		} else if len(tcp.Payload) == 0 {
			return nil
		}

		// TODO

	case c.conn.State == TCP_CONNECTION_FINISHED:
		c.conn.Instance.RemoveListener(c.conn)
	}

	return nil
}

type NetConnWrapper struct {
	*AckHandler
}

var _ net.Conn = &NetConnWrapper{}

func (c NetConnWrapper) Read(b []byte) (n int, err error) {
	// TODO: implement

	time.Sleep(time.Second)

	return 0, io.EOF
}

func (c NetConnWrapper) LocalAddr() net.Addr {
	_, src, _, srcPort, _ := c.AckHandler.conn.ConnectionTuple()

	return &net.TCPAddr{
		IP:   src,
		Port: int(srcPort),
	}
}

func (c NetConnWrapper) RemoteAddr() net.Addr {
	_, _, dst, _, dstPort := c.AckHandler.conn.ConnectionTuple()

	return &net.TCPAddr{
		IP:   dst,
		Port: int(dstPort),
	}
}

func (c NetConnWrapper) SetDeadline(t time.Time) error {
	return nil
}

func (c NetConnWrapper) SetReadDeadline(t time.Time) error {
	return nil
}

func (c NetConnWrapper) SetWriteDeadline(t time.Time) error {
	return nil
}
