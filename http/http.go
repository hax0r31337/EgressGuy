package http

import (
	"bytes"
	"crypto/rand"
	"log"
	"net/http"
	"sync"

	"golang.org/x/net/http2"
)

const (
	ALPN_HTTP1 = "http/1.1"
	ALPN_HTTP2 = "h2"
)

type HttpPayload struct {
	cachedHttp1Payload []byte
	cachedHttp2Payload []byte
	payloadLock        sync.Mutex

	Request     *http.Request
	NumRequests uint32
}

func NewHttpPayload(request *http.Request, requests uint32) HttpPayload {
	return HttpPayload{
		Request:     request,
		NumRequests: requests,
	}
}

func (h *HttpPayload) getHttp1Payload() []byte {
	var buf bytes.Buffer

	r := h.NumRequests

	for r > 0 {
		if r == 1 {
			h.Request.Header.Set("Connection", "close")
		} else {
			h.Request.Header.Set("Connection", "keep-alive")
		}

		if err := h.Request.Write(&buf); err != nil {
			log.Fatal(err)
		}

		r--
	}

	h.Request.Header.Del("Connection")

	return buf.Bytes()
}

func (h *HttpPayload) getHttp2Payload() []byte {
	var buf bytes.Buffer

	buf.WriteString(http2.ClientPreface)

	framer := http2.NewFramer(&buf, rand.Reader)

	if err := writeRequestToFramer(framer, h.Request, h.NumRequests); err != nil {
		log.Fatal(err)
	}

	return buf.Bytes()
}

func (h *HttpPayload) GetPayload(alpn string) []byte {
	h.payloadLock.Lock()
	defer h.payloadLock.Unlock()

	switch alpn {
	case ALPN_HTTP1:
		if h.cachedHttp1Payload == nil {
			h.cachedHttp1Payload = h.getHttp1Payload()
		}

		return h.cachedHttp1Payload
	case ALPN_HTTP2:
		if h.cachedHttp2Payload == nil {
			h.cachedHttp2Payload = h.getHttp2Payload()
		}

		return h.cachedHttp2Payload
	default:
		log.Println("unknown alpn:", alpn)
		return nil
	}
}
