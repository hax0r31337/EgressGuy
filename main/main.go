package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/hax0r31337/egressguy"
	ehttp "github.com/hax0r31337/egressguy/http"

	"github.com/gopacket/gopacket/layers"
	utls "github.com/refraction-networking/utls"
)

func main() {
	var workers, requests int
	var timeout time.Duration
	var method, request, userAgent, resolve, ifaceName string
	{
		var timeoutStr string

		flag.IntVar(&workers, "w", 50, "number of workers")
		flag.IntVar(&requests, "n", 3, "number of requests per connection")
		flag.StringVar(&timeoutStr, "t", "10s", "timeout")
		flag.StringVar(&method, "m", "GET", "method")
		flag.StringVar(&request, "r", "", "request url")
		flag.StringVar(&userAgent, "u", "EgressGuy/1.0", "user agent")
		flag.StringVar(&resolve, "d", "", "resolve override (file or ip)")
		flag.StringVar(&ifaceName, "i", "", "routing interface")

		flag.Parse()

		var err error
		timeout, err = time.ParseDuration(timeoutStr)
		if err != nil {
			log.Fatal(err)
		}
	}

	var payload ehttp.HttpPayload
	var addrs []net.IP
	var port layers.TCPPort
	var tlsConfig *utls.Config
	{
		req, err := http.NewRequest(method, request, nil)
		if err != nil {
			log.Fatal(err)
		}

		req.Header.Set("User-Agent", userAgent)

		payload = ehttp.NewHttpPayload(req, uint32(requests))

		// dns lookup
		if resolve == "" {
			addrs, err = net.DefaultResolver.LookupIP(context.Background(), "ip4", req.URL.Hostname())
			if err != nil {
				log.Fatal(err)
			} else if len(addrs) == 0 {
				log.Fatal("no ipv4 address found")
			}
		} else if addr := net.ParseIP(resolve); addr != nil {
			addrs = []net.IP{addr}
		} else {
			f, err := os.Open(resolve)
			if err != nil {
				log.Fatal(err)
			}

			addrs = make([]net.IP, 0)

			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				ip := net.ParseIP(scanner.Text())
				if ip == nil {
					log.Fatal("invalid ip address")
				}

				addrs = append(addrs, ip)
			}
		}

		switch strings.ToLower(req.URL.Scheme) {
		case "http":
			port = 80
		case "https":
			port = 443

			tlsConfig = &utls.Config{
				ServerName: req.URL.Hostname(),
			}
		default:
			log.Fatal("unsupported scheme")
		}

		if req.URL.Port() != "" {
			i, err := strconv.Atoi(req.URL.Port())
			if err != nil {
				log.Fatal(err)
			}

			port = layers.TCPPort(i)
		}
	}

	if len(addrs) == 0 {
		log.Fatal("no address found")
	} else if workers == 0 {
		log.Fatal("no workers")
	}

	iface, gw, src, err := egressguy.GetRoutingInfo(ifaceName, addrs[0])
	if err != nil {
		log.Fatal("routing error: ", err)
	}

	eg, err := egressguy.NewEgressGuy(iface, src, gw)
	if err != nil {
		log.Fatal(err)
	}
	defer eg.Close()

	var completed uint64

	go func() {
		t := time.NewTicker(1 * time.Second)
		start := time.Now()

		str := ANSI_ERASE_LINE + "traffic: " + ANSI_GREEN + "%s" + ANSI_RESET + "/s | total: " + ANSI_CYAN + "%s" + ANSI_RESET + " | completed: " + ANSI_PURPLE + "%d" + ANSI_RESET + " | %s" + ANSI_RESET

		var lastTraffic uint64

		for range t.C {
			total := eg.Traffic
			traf := total - lastTraffic
			lastTraffic = total

			fmt.Printf(str, egressguy.HumanizeBytes(traf), egressguy.HumanizeBytes(total), completed, time.Since(start).Round(time.Second).String())
		}
	}()

	var sourcePort layers.TCPPort
	var dialLock sync.Mutex

	rand.Read((*[2]byte)(unsafe.Pointer(uintptr(unsafe.Pointer(&sourcePort))))[:])

	for range workers {
		go func() {
			var handler egressguy.TcpHandler

			for {
				dialLock.Lock()
				sourcePort++
				addr := addrs[uint16(sourcePort)%uint16(len(addrs))]

				if tlsConfig != nil {
					h := egressguy.NewReliableReaderHandler()

					handler = h
				} else {
					h := egressguy.NewAckHandler()
					p := payload.GetPayload(ehttp.ALPN_HTTP1)
					if p == nil {
						continue
					}

					h.Write(p)

					handler = h
				}

				conn, err := egressguy.NewTcpConn(eg, src, addr, sourcePort, port, handler)
				dialLock.Unlock()
				if err != nil {
					log.Fatal(err)
				}

				timeoutChan := time.After(timeout)

				if tlsConfig != nil {
					h := handler.(*egressguy.ReliableReaderHandler)

					h.SetReadDeadline(time.Now().Add(timeout))

					w := egressguy.NetConnWrapper{
						ReliableReaderHandler: h,
					}

					tconn := utls.UClient(&w, tlsConfig, utls.HelloChrome_Auto)

					if err := tconn.Handshake(); err != nil {
						conn.Close()
						continue
					}

					alpn := tconn.ConnectionState().NegotiatedProtocol
					if alpn == "" {
						alpn = ehttp.ALPN_HTTP1
					}

					p := payload.GetPayload(alpn)
					if p == nil {
						conn.Close()
						continue
					}

					tconn.Write(p)

					w.ReliableReaderHandler = nil

					// switch to AckHandler
					conn.Win = 65535
					handler = egressguy.NewAckHandlerWithReliableWriterHandler(h.ReliableWriterHandler)
					conn.SetHandler(handler)

					tconn.Close()
				}

				select {
				case <-timeoutChan:
					conn.Close()
				case <-conn.OnClose():
					completed++
				}
			}
		}()
	}

	eg.ListenPackets()
}
