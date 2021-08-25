package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpproxy"
	"go.uber.org/automaxprocs/maxprocs"
)

var (
	startTime        = time.Now()
	sendOnCloseError interface{}
)

type ReportRecord struct {
	cost       time.Duration
	code       string
	error      string
	conn       string
	readBytes  int64
	writeBytes int64
}

var recordPool = sync.Pool{New: func() interface{} { return new(ReportRecord) }}

func init() {
	// Honoring env GOMAXPROCS
	_, _ = maxprocs.Set()
	defer func() {
		sendOnCloseError = recover()
	}()
	func() {
		cc := make(chan struct{}, 1)
		close(cc)
		cc <- struct{}{}
	}()
}

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

func ThroughputInterceptorDial(dial fasthttp.DialFunc, r *int64, w *int64) fasthttp.DialFunc {
	return func(addr string) (net.Conn, error) {
		conn, err := dial(addr)
		if err != nil {
			return nil, err
		}
		return NewMyConn(conn, r, w)
	}
}

type Requester struct {
	concurrency int
	verbose     int
	requests    int64
	duration    time.Duration
	clientOpt   *ClientOpt
	httpClient  *fasthttp.HostClient
	httpHeader  *fasthttp.RequestHeader

	recordChan chan *ReportRecord
	closeOnce  sync.Once
	wg         sync.WaitGroup

	readBytes  int64
	writeBytes int64
	logf       *os.File

	cancel   func()
	think    *ThinkTime
	logfLock *sync.Mutex

	// Qps is the rate limit in queries per second.
	QPS float64
}

type ClientOpt struct {
	url       string
	method    string
	headers   []string
	bodyBytes []byte
	bodyFile  string

	certPath string
	keyPath  string
	insecure bool

	maxConns     int
	doTimeout    time.Duration
	readTimeout  time.Duration
	writeTimeout time.Duration
	dialTimeout  time.Duration

	socks5Proxy string
	contentType string
	host        string
}

func NewRequester(concurrency, verbose int, requests int64, logf *os.File, duration time.Duration, clientOpt *ClientOpt, think *ThinkTime) (*Requester, error) {
	maxResult := concurrency * 100
	if maxResult > 8192 {
		maxResult = 8192
	}
	r := &Requester{
		concurrency: concurrency,
		requests:    requests,
		logf:        logf,
		duration:    duration,
		clientOpt:   clientOpt,
		recordChan:  make(chan *ReportRecord, maxResult),
		verbose:     verbose,
		QPS:         *qps,
	}

	if r.logf != nil {
		r.logfLock = &sync.Mutex{}
	}

	client, header, err := buildRequestClient(clientOpt, &r.readBytes, &r.writeBytes)
	if err != nil {
		return nil, err
	}
	r.httpClient = client
	r.httpHeader = header
	r.think = think
	return r, nil
}

func addMissingPort(addr string, isTLS bool) string {
	n := strings.Index(addr, ":")
	if n >= 0 {
		return addr
	}
	port := 80
	if isTLS {
		port = 443
	}
	return net.JoinHostPort(addr, strconv.Itoa(port))
}

func buildTLSConfig(opt *ClientOpt) (*tls.Config, error) {
	var certs []tls.Certificate
	if opt.certPath != "" && opt.keyPath != "" {
		c, err := tls.LoadX509KeyPair(opt.certPath, opt.keyPath)
		if err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	return &tls.Config{
		InsecureSkipVerify: opt.insecure,
		Certificates:       certs,
	}, nil
}

func buildRequestClient(opt *ClientOpt, r, w *int64) (*fasthttp.HostClient, *fasthttp.RequestHeader, error) {
	u, err := url.Parse(opt.url)
	if err != nil {
		return nil, nil, err
	}
	httpClient := &fasthttp.HostClient{
		Addr:         addMissingPort(u.Host, u.Scheme == "https"),
		IsTLS:        u.Scheme == "https",
		Name:         "blow",
		MaxConns:     opt.maxConns,
		ReadTimeout:  opt.readTimeout,
		WriteTimeout: opt.writeTimeout,

		DisableHeaderNamesNormalizing: true,
	}
	if opt.socks5Proxy != "" {
		if !strings.Contains(opt.socks5Proxy, "://") {
			opt.socks5Proxy = "socks5://" + opt.socks5Proxy
		}
		httpClient.Dial = fasthttpproxy.FasthttpSocksDialer(opt.socks5Proxy)
	} else {
		httpClient.Dial = fasthttpproxy.FasthttpProxyHTTPDialerTimeout(opt.dialTimeout)
	}
	httpClient.Dial = ThroughputInterceptorDial(httpClient.Dial, r, w)

	tlsConfig, err := buildTLSConfig(opt)
	if err != nil {
		return nil, nil, err
	}
	httpClient.TLSConfig = tlsConfig

	var requestHeader fasthttp.RequestHeader
	requestHeader.SetContentType(adjustContentType(opt))
	if opt.host != "" {
		requestHeader.SetHost(opt.host)
	} else {
		requestHeader.SetHost(u.Host)
	}

	requestHeader.SetMethod(adjustMethod(opt))
	requestHeader.SetRequestURI(u.RequestURI())
	for _, h := range opt.headers {
		n := strings.SplitN(h, ":", 2)
		if len(n) != 2 {
			return nil, nil, fmt.Errorf("invalid header: %s", h)
		}
		requestHeader.Set(n[0], n[1])
	}

	return httpClient, &requestHeader, nil
}

func adjustContentType(opt *ClientOpt) string {
	if opt.contentType != "" {
		return opt.contentType
	}

	if json.Valid(opt.bodyBytes) {
		return `application/json; charset=utf-8`
	}

	return `plain/text; charset=utf-8`
}

func adjustMethod(opt *ClientOpt) string {
	if opt.method != "" {
		return opt.method
	}

	if len(opt.bodyBytes) > 0 || opt.bodyFile != "" {
		return "POST"
	}

	return "GET"
}

func (r *Requester) Cancel()                          { r.cancel() }
func (r *Requester) RecordChan() <-chan *ReportRecord { return r.recordChan }
func (r *Requester) closeRecord()                     { r.closeOnce.Do(func() { close(r.recordChan) }) }

func (r *Requester) DoRequest(req *fasthttp.Request, rsp *fasthttp.Response, rr *ReportRecord) {
	t1 := time.Now()
	var err error
	if r.clientOpt.doTimeout > 0 {
		err = r.httpClient.DoTimeout(req, rsp, r.clientOpt.doTimeout)
	} else {
		err = r.httpClient.Do(req, rsp)
	}

	rr.cost = time.Since(t1)
	if err != nil {
		rr.code = ""
		rr.error = err.Error()
		return
	}

	rr.code = parseCodeNxx(rsp)

	if r.verbose >= 1 {
		rr.conn = rsp.LocalAddr().String() + "->" + rsp.RemoteAddr().String()
	}
	if r.logf != nil {
		err = r.logDetail(req, rsp, rr)
	} else {
		err = rsp.BodyWriteTo(ioutil.Discard)
	}

	if err != nil {
		rr.error = err.Error()
	} else {
		rr.error = ""
	}
}

func (r *Requester) logDetail(req *fasthttp.Request, rsp *fasthttp.Response, rr *ReportRecord) error {
	r.logfLock.Lock()
	defer r.logfLock.Unlock()

	_, _ = r.logf.WriteString(fmt.Sprintf("# %s time: %s cost: %s\n",
		rr.conn, time.Now().Format(time.RFC3339Nano), rr.cost))

	bw := bufio.NewWriter(r.logf)
	_ = req.Write(bw)
	_ = bw.Flush()
	_, _ = r.logf.Write([]byte("\n\n"))

	_, _ = r.logf.Write(rsp.Header.Header())
	err := rsp.BodyWriteTo(r.logf)
	_, _ = r.logf.Write([]byte("\n\n"))

	return err
}

func parseCodeNxx(resp *fasthttp.Response) string {
	switch resp.StatusCode() / 100 {
	case 1:
		return "1xx"
	case 2:
		return "2xx"
	case 3:
		return "3xx"
	case 4:
		return "4xx"
	case 5:
		return "5xx"
	default:
		return "?xx"
	}
}

func (r *Requester) Run() {
	// handle ctrl-c
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	ctx, cancelFunc := context.WithCancel(context.Background())
	r.cancel = cancelFunc
	go func() {
		<-sigs
		r.closeRecord()
		cancelFunc()
	}()
	startTime = time.Now()
	if r.duration > 0 {
		time.AfterFunc(r.duration, func() {
			r.closeRecord()
			cancelFunc()
		})
	}

	semaphore := r.requests
	for i := 0; i < r.concurrency; i++ {
		r.wg.Add(1)
		go func() {
			defer func() {
				r.wg.Done()
				v := recover()
				if v != nil && v != sendOnCloseError {
					panic(v)
				}
			}()
			req := &fasthttp.Request{}
			resp := &fasthttp.Response{}
			r.httpHeader.CopyTo(&req.Header)
			if r.httpClient.IsTLS {
				req.URI().SetScheme("https")
				req.URI().SetHostBytes(req.Header.Host())
			}

			throttle := func() {}
			if r.QPS > 0 {
				t := time.Tick(time.Duration(1e6/(r.QPS)) * time.Microsecond)
				throttle = func() { <-t }
			}

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				if r.requests > 0 && atomic.AddInt64(&semaphore, -1) < 0 {
					cancelFunc()
					return
				}

				throttle()

				if r.clientOpt.bodyFile != "" {
					file, err := os.Open(r.clientOpt.bodyFile)
					if err != nil {
						rr := recordPool.Get().(*ReportRecord)
						rr.cost = 0
						rr.error = err.Error()
						rr.readBytes = atomic.LoadInt64(&r.readBytes)
						rr.writeBytes = atomic.LoadInt64(&r.writeBytes)
						r.recordChan <- rr
						continue
					}
					req.SetBodyStream(file, -1)
				} else {
					req.SetBodyRaw(r.clientOpt.bodyBytes)
				}
				resp.Reset()
				rr := recordPool.Get().(*ReportRecord)
				r.DoRequest(req, resp, rr)
				rr.readBytes = atomic.LoadInt64(&r.readBytes)
				rr.writeBytes = atomic.LoadInt64(&r.writeBytes)
				r.recordChan <- rr
				if r.think != nil {
					r.think.Think(true)
				}
			}
		}()
	}

	r.wg.Wait()
	r.closeRecord()
}
