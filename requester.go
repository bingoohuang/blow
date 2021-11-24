package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

	"github.com/bingoohuang/blow/profile"
	"github.com/bingoohuang/blow/util"

	"github.com/bingoohuang/gg/pkg/thinktime"

	"github.com/bingoohuang/jj"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpproxy"
	"go.uber.org/automaxprocs/maxprocs"
)

var (
	startTime        = time.Now()
	sendOnCloseError interface{}
)

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

type Requester struct {
	concurrency int
	verbose     int
	requests    int64
	duration    time.Duration
	clientOpt   *ClientOpt
	httpClient  *fasthttp.HostClient
	httpHeader  *fasthttp.RequestHeader

	recordChan chan *ReportRecord
	uploadChan chan string

	closeOnce sync.Once
	wg        sync.WaitGroup

	readBytes  int64
	writeBytes int64

	logf *util.LogFile

	cancel func()
	think  *thinktime.ThinkTime

	// Qps is the rate limit in queries per second.
	QPS float64

	upload          string
	uploadFileField string
	statusName      string
	noUploadCache   bool
	ctx             context.Context

	httpClientDo func(*fasthttp.Request, *fasthttp.Response) error
	profiles     []*profile.Profile
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
	upload      string

	basicAuth string
	network   string
}

func NewRequester(concurrency, verbose int, requests int64, duration time.Duration, clientOpt *ClientOpt,
	statusName string, profiles []*profile.Profile) (*Requester, error) {
	maxResult := concurrency * 100
	if maxResult > 8192 {
		maxResult = 8192
	}

	ctx, cancelFunc := context.WithCancel(context.Background())

	r := &Requester{
		concurrency: concurrency,
		requests:    requests,
		duration:    duration,
		clientOpt:   clientOpt,
		recordChan:  make(chan *ReportRecord, maxResult),
		verbose:     verbose,
		QPS:         *qps,
		statusName:  statusName,
		ctx:         ctx,
		cancel:      cancelFunc,
		profiles:    profiles,
	}

	client, header, err := r.buildRequestClient(clientOpt)
	if err != nil {
		return nil, err
	}
	r.httpClient = client
	r.httpHeader = header

	if clientOpt.upload != "" {
		const nocacheTag = ":nocache"
		if strings.HasSuffix(clientOpt.upload, nocacheTag) {
			r.noUploadCache = true
			clientOpt.upload = strings.TrimSuffix(clientOpt.upload, nocacheTag)
		}

		if pos := strings.IndexRune(clientOpt.upload, ':'); pos > 0 {
			r.uploadFileField = clientOpt.upload[:pos]
			r.upload = clientOpt.upload[pos+1:]
		} else {
			r.uploadFileField = "file"
			r.upload = clientOpt.upload
		}
	}

	if r.upload != "" {
		r.uploadChan = make(chan string, 1)
		go util.DealUploadFilePath(ctx, r.upload, r.uploadChan)
	}

	return r, nil
}

func addMissingPort(addr string, isTLS bool) string {
	if addr == "" {
		return ""
	}

	if n := strings.Index(addr, ":"); n >= 0 {
		return addr
	}
	p := 80
	if isTLS {
		p = 443
	}
	return net.JoinHostPort(addr, strconv.Itoa(p))
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

func (r *Requester) buildRequestClient(opt *ClientOpt) (*fasthttp.HostClient, *fasthttp.RequestHeader, error) {
	var u *url.URL
	var err error

	if opt.url != "" {
		u, err = url.Parse(opt.url)
	} else if len(r.profiles) > 0 {
		u, err = url.Parse(r.profiles[0].URL)
	}

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

	httpClient.Dial = ThroughputStatDial(networkWrap(opt.network), httpClient.Dial, &r.readBytes, &r.writeBytes)

	tlsConfig, err := buildTLSConfig(opt)
	if err != nil {
		return nil, nil, err
	}
	httpClient.TLSConfig = tlsConfig

	var h fasthttp.RequestHeader
	h.SetContentType(adjustContentType(opt))
	if opt.host != "" {
		h.SetHost(opt.host)
	} else {
		h.SetHost(u.Host)
	}

	h.SetMethod(adjustMethod(opt))
	h.SetRequestURI(u.RequestURI())
	for _, v := range opt.headers {
		n := strings.SplitN(v, ":", 2)
		if len(n) != 2 {
			return nil, nil, fmt.Errorf("invalid header: %s", v)
		}
		h.Set(n[0], n[1])
	}

	if opt.basicAuth != "" {
		h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(opt.basicAuth)))
	}

	return httpClient, &h, nil
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

	if opt.upload != "" || len(opt.bodyBytes) > 0 || opt.bodyFile != "" {
		return "POST"
	}

	return "GET"
}

func (r *Requester) Cancel()                          { r.cancel() }
func (r *Requester) RecordChan() <-chan *ReportRecord { return r.recordChan }
func (r *Requester) closeRecord() {
	r.closeOnce.Do(func() {
		close(r.recordChan)
	})
}

func (r *Requester) doRequest(req *fasthttp.Request, rsp *fasthttp.Response, rr *ReportRecord) (err error) {
	t1 := time.Now()
	err = r.httpClientDo(req, rsp)
	rr.cost = time.Since(t1)
	if err != nil {
		return err
	}

	rr.code = []string{parseCodeNxx(rsp, r.statusName)}
	if r.verbose >= 1 {
		rr.conn = []string{rsp.LocalAddr().String() + "->" + rsp.RemoteAddr().String()}
	}
	if r.logf != nil {
		return r.logDetail(req, rsp, rr)
	}

	return rsp.BodyWriteTo(ioutil.Discard)
}

func (r *Requester) logDetail(req *fasthttp.Request, rsp *fasthttp.Response, rr *ReportRecord) error {
	b := &bytes.Buffer{}
	defer r.logf.Write(b)

	conn := rsp.LocalAddr().String() + "->" + rsp.RemoteAddr().String()
	_, _ = b.WriteString(fmt.Sprintf("### %s time: %s cost: %s\n",
		conn, time.Now().Format(time.RFC3339Nano), rr.cost))

	bw := bufio.NewWriter(b)
	_ = req.Write(bw)
	_ = bw.Flush()
	_, _ = b.Write([]byte("\n"))

	_, _ = b.Write(rsp.Header.Header())

	if string(rsp.Header.Peek("Content-Encoding")) == "gzip" {
		bodyGunzip, err := rsp.BodyGunzip()
		if err != nil {
			return err
		}
		b.Write(bodyGunzip)
	} else {
		if err := rsp.BodyWriteTo(b); err != nil {
			return err
		}
	}

	_, _ = b.Write([]byte("\n\n"))
	return nil
}

func parseCodeNxx(resp *fasthttp.Response, statusName string) string {
	if statusName == "" {
		return strconv.Itoa(resp.StatusCode())
	}

	return jj.GetBytes(resp.Body(), statusName).String()
}

func (r *Requester) Run() {
	// handle ctrl-c
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	go func() {
		<-sigs
		r.closeRecord()
		r.cancel()
	}()
	startTime = time.Now()
	if r.duration > 0 {
		time.AfterFunc(r.duration, func() {
			r.closeRecord()
			r.cancel()
		})
	}

	if r.clientOpt.doTimeout > 0 {
		r.httpClientDo = func(req *fasthttp.Request, rsp *fasthttp.Response) error {
			return r.httpClient.DoTimeout(req, rsp, r.clientOpt.doTimeout)
		}
	} else {
		r.httpClientDo = r.httpClient.Do
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
			req := fasthttp.AcquireRequest()
			resp := fasthttp.AcquireResponse()
			defer fasthttp.ReleaseRequest(req)
			defer fasthttp.ReleaseResponse(resp)

			if len(r.profiles) == 0 {
				r.httpHeader.CopyTo(&req.Header)
				if r.httpClient.IsTLS {
					req.URI().SetScheme("https")
					req.URI().SetHostBytes(req.Header.Host())
				}

				if *enableGzip {
					req.Header.Set("Accept-Encoding", "gzip")
				}
			}

			throttle := func() {}
			if r.QPS > 0 {
				t := time.Tick(time.Duration(1e6/(r.QPS)) * time.Microsecond)
				throttle = func() { <-t }
			}

			for r.ctx.Err() == nil {
				if r.requests > 0 && atomic.AddInt64(&semaphore, -1) < 0 {
					r.cancel()
					return
				}

				throttle()

				rr := recordPool.Get().(*ReportRecord)
				rr.Reset()

				if r.logf != nil {
					r.logf.MarkPos()
				}

				if len(r.profiles) == 0 {
					r.runOne(req, resp, rr)
				} else {
					r.runProfiles(req, resp, rr)
				}

				r.recordChan <- rr

				r.thinking()
			}
		}()
	}

	r.wg.Wait()
	r.closeRecord()
}

func (r *Requester) runOne(req *fasthttp.Request, resp *fasthttp.Response, rr *ReportRecord) *ReportRecord {
	closers, err := r.setBody(req)
	defer closers.Close()

	if err == nil {
		err = r.doRequest(req, resp, rr)
	}

	if err != nil {
		rr.error = err.Error()
	}

	rr.readBytes = atomic.LoadInt64(&r.readBytes)
	rr.writeBytes = atomic.LoadInt64(&r.writeBytes)
	return rr
}

func (r *Requester) runProfiles(req *fasthttp.Request, rsp *fasthttp.Response, rr *ReportRecord) {
	for _, p := range r.profiles {
		if err := r.runOneProfile(p, req, rsp, rr); err != nil {
			rr.error = err.Error()
			return
		}

		if rsp.StatusCode() < 200 || rsp.StatusCode() > 300 {
			return
		}

		req.Reset()
		rsp.Reset()
	}
}

func (r *Requester) runOneProfile(p *profile.Profile, req *fasthttp.Request, rsp *fasthttp.Response, rr *ReportRecord) error {
	closers, err := p.CreateReq(r.httpClient, req, *enableGzip)
	defer closers.Close()

	if err != nil {
		return err
	}

	t1 := time.Now()
	err = r.httpClientDo(req, rsp)
	rr.cost += time.Since(t1)
	if err != nil {
		return err
	}

	rr.code = append(rr.code, parseCodeNxx(rsp, r.statusName))
	if r.verbose >= 1 {
		rr.conn = append(rr.conn, rsp.LocalAddr().String()+"->"+rsp.RemoteAddr().String())
	}
	if r.logf != nil {
		return r.logDetail(req, rsp, rr)
	}

	return rsp.BodyWriteTo(ioutil.Discard)
}

func (r *Requester) thinking() {
	if r.think != nil {
		r.think.Think(true)
	}
}

func (r *Requester) setBody(req *fasthttp.Request) (util.Closers, error) {
	if r.clientOpt.bodyFile != "" {
		file, err := os.Open(r.clientOpt.bodyFile)
		if err != nil {
			return nil, err
		}
		req.SetBodyStream(file, -1)
		return []io.Closer{file}, nil
	}
	if r.upload != "" {
		file := <-r.uploadChan
		data, cType, err := util.ReadMultipartFile(r.noUploadCache, r.uploadFileField, file)
		if err != nil {
			panic(err)
		}
		util.SetHeader(req, "Content-Type", cType)
		req.SetBody(data)
		return nil, nil
	}

	bodyBytes := r.clientOpt.bodyBytes

	if *enableGzip {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		zw.Write(bodyBytes)
		zw.Close()
		if v := buf.Bytes(); len(v) < len(bodyBytes) {
			bodyBytes = v
			req.Header.Set("Content-Encoding", "gzip")
		}
	}

	req.SetBodyRaw(bodyBytes)
	return nil, nil
}
