package http

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

const (
	MAX_FRAME_SIZE = 16 << 10
)

// write http2 request with the fingerprint of Chrome 128
func writeRequestToFramer(framer *http2.Framer, req *http.Request, requests uint32) error {
	framer.WriteSettings(http2.Setting{
		ID:  http2.SettingHeaderTableSize,
		Val: 65536,
	}, http2.Setting{
		ID:  http2.SettingEnablePush,
		Val: 0,
	}, http2.Setting{
		ID:  http2.SettingInitialWindowSize,
		Val: 6291456,
	}, http2.Setting{
		ID:  http2.SettingMaxHeaderListSize,
		Val: 262144,
	})

	framer.WriteWindowUpdate(0, 15663105)

	headers, err := writeHeaders(req)
	if err != nil {
		return err
	}

	endStream := req.Body == nil || req.Body == http.NoBody

	for i := range requests {
		writeHeadersToFramer(framer, i*2+1, endStream, headers)

		if !endStream {
			writeBodyToFramer(framer, i*2+1, req)
		}
	}

	return nil
}

func writeHeadersToFramer(framer *http2.Framer, streamId uint32, endStream bool, headers []byte) {
	first := true

	for len(headers) > 0 {
		chunk := headers[:min(MAX_FRAME_SIZE, len(headers))]
		headers = headers[len(chunk):]

		endHeaders := len(headers) == 0

		if first {
			framer.WriteHeaders(http2.HeadersFrameParam{
				StreamID:      streamId,
				BlockFragment: chunk,
				EndStream:     endStream,
				EndHeaders:    endHeaders,
				Priority: http2.PriorityParam{
					StreamDep: 0,
					Exclusive: true,
					Weight:    255,
				},
			})
			first = false
		} else {
			framer.WriteContinuation(streamId, endHeaders, chunk)
		}
	}
}

func writeBodyToFramer(framer *http2.Framer, streamId uint32, req *http.Request) {
	hasEndStream := false
	defer func() {
		if !hasEndStream {
			framer.WriteData(streamId, true, nil)
		}
	}()

	buf := make([]byte, MAX_FRAME_SIZE)

	for {
		n, err := req.Body.Read(buf)
		if err != nil {
			break
		}

		endStream := n != MAX_FRAME_SIZE
		framer.WriteData(streamId, endStream, buf[:n])
		if endStream {
			hasEndStream = true
			break
		}
	}
}

func writeHeaders(req *http.Request) ([]byte, error) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	host, err := httpguts.PunycodeHostPort(host)
	if err != nil {
		return nil, err
	}

	if req.Trailer != nil || len(req.Trailer) > 0 {
		return nil, errors.New("http2: trailers not supported")
	}

	var buf bytes.Buffer

	henc := hpack.NewEncoder(&buf)

	writeHeader := func(name, value string) {
		henc.WriteField(hpack.HeaderField{Name: name, Value: value})
	}

	writeHeader(":method", req.Method)
	writeHeader(":authority", host)
	writeHeader(":scheme", "https")
	writeHeader(":path", req.URL.RequestURI())

	for k, vv := range req.Header {
		lower := strings.ToLower(k)

		if lower == "host" || lower == "content-length" {
			continue
		} else if lower == "connection" ||
			lower == "proxy-connection" ||
			lower == "transfer-encoding" ||
			lower == "upgrade" ||
			lower == "keep-alive" {
			// Per 8.1.2.2 Connection-Specific Header
			// Fields, don't send connection-specific
			// fields. We have already checked if any
			// are error-worthy so just ignore the rest.
			continue
		} else if lower == "cookie" {
			// Per 8.1.2.5 To allow for better compression efficiency, the
			// Cookie header field MAY be split into separate header fields,
			// each with one or more cookie-pairs.
			for _, v := range vv {
				for {
					p := strings.IndexByte(v, ';')
					if p < 0 {
						break
					}
					writeHeader("cookie", v[:p])
					p++
					// strip space after semicolon if any.
					for p+1 <= len(v) && v[p] == ' ' {
						p++
					}
					v = v[p:]
				}
				if len(v) > 0 {
					writeHeader("cookie", v)
				}
			}
		}

		for _, v := range vv {
			writeHeader(lower, v)
		}
	}

	if cl, send := getContentLength(req); send {
		writeHeader("content-length", strconv.FormatInt(cl, 10))
	}

	return buf.Bytes(), nil
}

func getContentLength(req *http.Request) (int64, bool) {
	if req.Method != "POST" && req.Method != "PUT" && req.Method != "PATCH" {
		return 0, false
	}

	if req.Body == nil || req.Body == http.NoBody {
		return 0, true
	}

	if req.ContentLength > 0 {
		return req.ContentLength, true
	} else if req.ContentLength < 0 {
		return 0, false
	}

	return 0, true
}
