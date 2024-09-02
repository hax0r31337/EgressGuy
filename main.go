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

	ehttp "egressguy/http"

	utls "github.com/refraction-networking/utls"

	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/routing"
)

func main() {
	var workers, requests int
	var timeout time.Duration
	var method, request, userAgent, resolve string
	{
		var timeoutStr string

		flag.IntVar(&workers, "w", 50, "number of workers")
		flag.IntVar(&requests, "n", 1, "number of requests per connection")
		flag.StringVar(&timeoutStr, "t", "7s", "timeout")
		flag.StringVar(&method, "m", "GET", "method")
		flag.StringVar(&request, "r", "", "request url")
		flag.StringVar(&userAgent, "u", "EgressGuy/1.0", "user agent")
		flag.StringVar(&resolve, "d", "", "resolve override (file or ip)")

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

	router, err := routing.New()
	if err != nil {
		log.Fatal("routing error: ", err)
	}

	iface, gw, src, err := router.Route(net.IPv4(8, 9, 6, 4))
	if err != nil {
		log.Fatal(err)
	}

	eg, err := NewEgressGuy(iface, src, gw)
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

			fmt.Printf(str, HumanizeBytes(traf), HumanizeBytes(total), completed, time.Since(start).String())
		}
	}()

	var sourcePort layers.TCPPort
	var dialLock sync.Mutex

	rand.Read((*[2]byte)(unsafe.Pointer(uintptr(unsafe.Pointer(&sourcePort))))[:])

	for range workers {
		go func() {
			var handler TcpHandler

			for {
				dialLock.Lock()
				sourcePort++
				addr := addrs[uint16(sourcePort)%uint16(len(addrs))]

				if tlsConfig != nil {
					log.Fatal("not implemented")
					// h := NewAckHandler()

					// go func() {
					// 	w := NetConnWrapper{
					// 		AckHandler: h,
					// 	}

					// 	conn := utls.UClient(&w, tlsConfig, utls.HelloChrome_120)

					// 	if err := conn.Handshake(); err != nil {
					// 		log.Println("\n TLS ERR:", err)
					// 	}
					// }()

					// handler = h
				} else {
					h := NewAckHandler()
					p := payload.GetPayload(ehttp.ALPN_HTTP1)
					if p == nil {
						continue
					}

					h.Write(p)

					handler = h
				}

				conn, err := NewTcpConn(eg, src, addr, sourcePort, port, handler)
				dialLock.Unlock()
				if err != nil {
					log.Fatal(err)
				}

				select {
				case <-time.After(timeout):
					conn.Close()
				case <-conn.onClose:
					completed++
				}
			}
		}()
	}

	eg.ListenPackets()
}
