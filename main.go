package main

import (
	"bytes"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/routing"
)

func main() {
	var workers int
	var timeout time.Duration
	var method, request, userAgent string
	{
		var timeoutStr string

		flag.IntVar(&workers, "w", 50, "number of workers")
		flag.StringVar(&timeoutStr, "t", "7s", "timeout")
		flag.StringVar(&method, "m", "GET", "method")
		flag.StringVar(&request, "r", "", "request")
		flag.StringVar(&userAgent, "u", "EgressGuy/1.0", "user agent")

		flag.Parse()

		var err error
		timeout, err = time.ParseDuration(timeoutStr)
		if err != nil {
			log.Fatal(err)
		}
	}

	var payload []byte
	var addr net.IP
	var port layers.TCPPort
	{
		req, err := http.NewRequest(method, request, nil)
		if err != nil {
			log.Fatal(err)
		}

		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Connection", "close")

		var buf bytes.Buffer

		if err := req.Write(&buf); err != nil {
			log.Fatal(err)
		}

		payload = buf.Bytes()

		// dns lookup
		addrs, err := net.LookupIP(req.Host)
		if err != nil {
			log.Fatal(err)
		}

		for _, a := range addrs {
			if a.To4() != nil {
				addr = a.To4()
				break
			}
		}

		if addr == nil {
			log.Fatal("no ipv4 address found")
		}

		if req.URL.Port() == "" {
			port = 80
		} else {
			i, err := strconv.Atoi(req.URL.Port())
			if err != nil {
				log.Fatal(err)
			}

			port = layers.TCPPort(i)
		}
	}

	router, err := routing.New()
	if err != nil {
		log.Fatal("routing error:", err)
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
			for {
				dialLock.Lock()
				sourcePort++
				conn, err := NewTcpConn(eg, src, addr, sourcePort, port, payload)
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
