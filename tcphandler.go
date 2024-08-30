package main

import (
	"log"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

type TcpHandler interface {
	SetConn(conn *TcpConn)
	HandlePacket(packet gopacket.Packet, layer gopacket.Layer)
}

// sends some data, then acks incoming data
// suitable for protocols without handshake, such as HTTP
type PayloadAckHandler struct {
	conn *TcpConn

	payload []byte

	counter     uint8
	lastCounter uint8
	cachedAck   layers.TCP
}

func NewPayloadAckHandler(payload []byte) *PayloadAckHandler {
	return &PayloadAckHandler{
		payload: payload,
	}
}

func (c *PayloadAckHandler) SetConn(conn *TcpConn) {
	c.conn = conn

	c.cachedAck = conn.NewPacket()
	c.cachedAck.ACK = true
}

func (c *PayloadAckHandler) HandlePacket(packet gopacket.Packet, layer gopacket.Layer) {
	tcp := layer.(*layers.TCP)

	switch {
	case c.conn.State == TCP_CONNECTION_ESTABLISHED:
		if tcp.FIN {
			c.conn.setClosed(false)
		} else if tcp.RST {
			c.conn.setClosed(false)
			return
		} else if len(tcp.Payload) == 0 {
			return
		} else if c.counter != 0 {
			c.counter--

			if c.conn.Ack+uint32(len(tcp.Payload))+uint32(c.conn.Mss)*2 > tcp.Seq {
				return
			} else {
				c.lastCounter = 1
			}
		}
		c.counter = c.lastCounter + 1
		c.lastCounter = max(c.counter, 5)

		c.conn.Ack = tcp.Seq

		c.cachedAck.Seq = c.conn.Seq
		c.cachedAck.Ack = c.conn.Ack

		if err := c.conn.SendPacket(&c.cachedAck, nil); err != nil {
			log.Println("error sending ACK:", err)
		}
	case c.conn.State == TCP_CONNECTION_SYN_SENT && tcp.SYN && tcp.ACK:
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

		c.conn.Write(c.payload)
	case c.conn.State == TCP_CONNECTION_FINISHED:
		c.conn.Instance.RemoveListener(c.conn)
	}
}
