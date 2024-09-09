package http

import (
	"bytes"
	"crypto/rand"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

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

	var body []byte
	if h.Request.Body != nil {
		var err error
		body, err = getRequestBodyBytes(h.Request)
		if err != nil {
			log.Fatal(err)
		}
	}

	for r > 0 {
		if r == 1 {
			h.Request.Header.Set("Connection", "close")
		} else {
			h.Request.Header.Set("Connection", "keep-alive")
		}

		if err := h.Request.Write(&buf); err != nil {
			log.Fatal(err)
		}

		if body != nil {
			h.Request.Body = io.NopCloser(bytes.NewReader(body))
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
	switch alpn {
	case ALPN_HTTP1:
		if h.cachedHttp1Payload == nil {
			h.payloadLock.Lock()
			if h.cachedHttp1Payload == nil {
				h.cachedHttp1Payload = h.getHttp1Payload()
			}
			h.payloadLock.Unlock()
		}

		return h.cachedHttp1Payload
	case ALPN_HTTP2:
		if h.cachedHttp2Payload == nil {
			h.payloadLock.Lock()
			if h.cachedHttp2Payload == nil {
				h.cachedHttp2Payload = h.getHttp2Payload()
			}
			h.payloadLock.Unlock()
		}

		return h.cachedHttp2Payload
	default:
		log.Println("unknown alpn:", alpn)
		return nil
	}
}

func ProbeRedirects(req *http.Request) error {
	var err error
	var resp *http.Response

	client := http.Client{
		// we want to handle redirects manually
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 5 * time.Second,
	}

	for resp == nil || resp.Header.Get("Location") != "" {
		resp, err = client.Do(req)
		if err != nil {
			return err
		}

		if location := resp.Header.Get("Location"); location != "" {
			u, err := url.Parse(location)
			if err != nil {
				return err
			}

			req.URL = u
			req.Host = u.Host
		}
	}

	return nil
}

func getRequestBodyBytes(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}

	req.Body = io.NopCloser(bytes.NewReader(body))

	return body, nil
}
