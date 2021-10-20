package main

import (
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bingoohuang/blow/latency"
	"github.com/valyala/fasthttp"
)

type MyConn struct {
	net.Conn
	r, w *int64
}

func NewMyConn(conn net.Conn, r, w *int64) (*MyConn, error) {
	return &MyConn{Conn: conn, r: r, w: w}, nil
}

func (c *MyConn) Read(b []byte) (n int, err error) {
	if n, err = c.Conn.Read(b); n > 0 {
		atomic.AddInt64(c.r, int64(n))
	}
	return
}

func (c *MyConn) Write(b []byte) (n int, err error) {
	if n, err = c.Conn.Write(b); n > 0 {
		atomic.AddInt64(c.w, int64(n))
	}
	return
}

func ThroughputInterceptorDial(network string, dial fasthttp.DialFunc, r *int64, w *int64) fasthttp.DialFunc {
	return func(addr string) (net.Conn, error) {
		conn, err := dial(addr)
		if err != nil {
			return nil, err
		}

		if network != "" {
			n := parseNetwork(network)
			if conn, err = n.Conn(conn); err != nil {
				return nil, err
			}
		}
		return NewMyConn(conn, r, w)
	}
}

func parseNetwork(network string) latency.Network {
	switch strings.ToLower(network) {
	case "local":
		return latency.Local
	case "lan":
		return latency.LAN
	case "wan":
		return latency.WAN
	case "longhaul":
		return latency.Longhaul
	default:
		parts := strings.SplitN(network, ":", 3)
		kbps, _ := strconv.Atoi(parts[0])
		delay, _ := time.ParseDuration(parts[1])
		mtu, _ := strconv.Atoi(parts[2])

		return latency.Network{kbps, delay, mtu}
	}
}
