package http

import (
	"bytes"
	"crypto/rand"
	"log"
	"net/http"

	"golang.org/x/net/http2"
)

const (
	ALPN_HTTP1 = "http/1.1"
	ALPN_HTTP2 = "h2"
)

type HttpPayload struct {
	cachedHttp1Payload []byte
	cachedHttp2Payload []byte

	Request *http.Request
}

func NewHttpPayload(request *http.Request) HttpPayload {
	return HttpPayload{
		Request: request,
	}
}

func (h *HttpPayload) getHttp1Payload() []byte {
	var buf bytes.Buffer

	if err := h.Request.Write(&buf); err != nil {
		log.Fatal(err)
	}

	return buf.Bytes()
}

func (h *HttpPayload) getHttp2Payload() []byte {
	var buf bytes.Buffer

	buf.WriteString(http2.ClientPreface)

	framer := http2.NewFramer(&buf, rand.Reader)

	if err := writeRequestToFramer(framer, h.Request); err != nil {
		log.Fatal(err)
	}

	return buf.Bytes()
}

func (h *HttpPayload) GetPayload(alpn string) []byte {
	switch alpn {
	case "http/1.1":
		if h.cachedHttp1Payload == nil {
			h.cachedHttp1Payload = h.getHttp1Payload()
		}

		return h.cachedHttp1Payload
	case "h2":
		if h.cachedHttp2Payload == nil {
			h.cachedHttp2Payload = h.getHttp2Payload()
		}

		return h.cachedHttp2Payload
	default:
		log.Println("unknown alpn:", alpn)
		return nil
	}
}
