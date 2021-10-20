package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/alecthomas/kingpin.v3-unstable"
)

var (
	flag = kingpin.Flag

	concurrency = flag("concurrency", "#connections to run concurrently").Short('c').Default("100").Int()
	requests    = flag("requests", "#requests to run").Short('n').Default("-1").Int64()
	duration    = flag("duration", "Duration of test, examples: -d10s -d3m").Short('d').PlaceHolder("DURATION").Duration()
	verbose     = flag("verbose", "v: Show connections in summary. vv: Log requests and response details to file").Short('v').Counter()
	thinkTime   = flag("think", "Think time among requests, eg. 1s, 10ms, 10-20ms and etc. (unit ns, us/µs, ms, s, m, h)").PlaceHolder("DURATION").String()

	body        = flag("body", "HTTP request body, or @file to read from").Short('b').String()
	upload      = flag("upload", "HTTP upload multipart form file or directory, or add prefix file: to set form field name ").Short('u').String()
	qps         = flag("qps", "Rate limit, in queries per second per worker. Default is no rate limit").Short('q').Float64()
	stream      = flag("stream", "Specify whether to stream file specified by '--body @file' using chunked encoding or to read into memory").Default("false").Bool()
	method      = flag("method", "HTTP method").Short('m').String()
	headers     = flag("header", "Custom HTTP headers").Short('H').PlaceHolder("K:V").Strings()
	host        = flag("host", "Host header").String()
	basicAuth   = flag("user", "basic auth username:password").String()
	contentType = flag("content", "Content-Type header").Short('T').String()
	cert        = flag("cert", "Path to the client's TLS Certificate").ExistingFile()
	key         = flag("key", "Path to the client's TLS Certificate Private Key").ExistingFile()
	insecure    = flag("insecure", "Controls whether a client verifies the server's certificate chain and host name").Short('k').Bool()
	reportType  = flag("report", "How to report, dynamic (default) / json").Enum("json", "dynamic")

	port            = flag("port", "Listen port for serve Web UI").Default("18888").Int()
	timeout         = flag("timeout", "Timeout for each http request").PlaceHolder("DURATION").Duration()
	dialTimeout     = flag("dial-timeout", "Timeout for dial addr").PlaceHolder("DURATION").Duration()
	reqWriteTimeout = flag("req-timeout", "Timeout for full request writing").PlaceHolder("DURATION").Duration()
	rspReadTimeout  = flag("rsp-timeout", "Timeout for full response reading").PlaceHolder("DURATION").Duration()
	socks5          = flag("socks5", "Socks5 proxy").PlaceHolder("ip:port").String()
	autoOpenBrowser = flag("auto-open-browser", "Specify whether auto open browser to show Web charts").Bool()

	urlAddr = kingpin.Arg("url", "request url").String()
)

func errAndExit(msg string) {
	fmt.Fprintln(os.Stderr, "blow: "+msg)
	os.Exit(1)
}

var CompactUsageTemplate = `{{define "FormatCommand" -}}
{{if .FlagSummary}} {{.FlagSummary}}{{end -}}
{{range .Args}} {{if not .Required}}[{{end}}<{{.Name}}>{{if .Value|IsCumulative}} ...{{end}}{{if not .Required}}]{{end}}{{end -}}
{{end -}}

{{define "FormatCommandList" -}}
{{range . -}}
{{if not .Hidden -}}
{{.Depth|Indent}}{{.Name}}{{if .Default}}*{{end}}{{template "FormatCommand" .}}
{{end -}}
{{template "FormatCommandList" .Commands -}}
{{end -}}
{{end -}}

{{define "FormatUsage" -}}
{{template "FormatCommand" .}}{{if .Commands}} <command> [<args> ...]{{end}}
{{if .Help}}
{{.Help|Wrap 0 -}}
{{end -}}

{{end -}}

{{if .Context.SelectedCommand -}}
{{T "usage:"}} {{.App.Name}} {{template "FormatUsage" .Context.SelectedCommand}}
{{else -}}
{{T "usage:"}} {{.App.Name}}{{template "FormatUsage" .App}}
{{end -}}
Examples:

  blow :18888/api/hello
  blow http://127.0.0.1:8080/ -c 20 -n 100000
  blow https://httpbin.org/post -c 20 -d 5m --body @file.json -T 'application/json' -m POST

{{if .Context.Flags -}}
{{T "Flags:"}}
{{.Context.Flags|FlagsToTwoColumns|FormatTwoColumns}}
  Flags default values also read from env BLOW_SOME_FLAG, such as BLOW_TIMEOUT=5s equals to --timeout=5s

{{end -}}
{{if .Context.Args -}}
{{T "Args:"}}
{{.Context.Args|ArgsToTwoColumns|FormatTwoColumns}}
{{end -}}
{{if .Context.SelectedCommand -}}
{{if .Context.SelectedCommand.Commands -}}
{{T "Commands:"}}
  {{.Context.SelectedCommand}}
{{.Context.SelectedCommand.Commands|CommandsToTwoColumns|FormatTwoColumns}}
{{end -}}
{{else if .App.Commands -}}
{{T "Commands:"}}
{{.App.Commands|CommandsToTwoColumns|FormatTwoColumns}}
{{end -}}
`

func main() {
	kingpin.UsageTemplate(CompactUsageTemplate).
		Version(Version()).
		Author("bingoohuang@github").
		Resolver(kingpin.PrefixedEnvarResolver("BLOW_", ";")).
		Help = `A high-performance HTTP benchmarking tool with real-time web UI and terminal displaying`
	kingpin.Parse()

	if *urlAddr != "" {
		if v, err := FixURI(*urlAddr); err != nil {
			errAndExit(err.Error())
		} else {
			*urlAddr = v
		}
	} else {
		*requests = 0
	}

	if *requests == 1 {
		*verbose = 2
	}

	if *requests > 0 && *requests < int64(*concurrency) {
		*concurrency = int(*requests)
	}

	if *cert == "" || *key == "" {
		*cert = ""
		*key = ""
	}

	logf := createLogFile()
	think, err := ParseThinkTime(*thinkTime)
	if err != nil {
		errAndExit(err.Error())
	}

	bodyFile, bodyBytes := parseBodyArg()

	clientOpt := ClientOpt{
		url:       *urlAddr,
		method:    *method,
		headers:   *headers,
		bodyBytes: bodyBytes,
		bodyFile:  bodyFile,

		certPath: *cert,
		keyPath:  *key,
		insecure: *insecure,

		maxConns:     *concurrency,
		doTimeout:    *timeout,
		readTimeout:  *rspReadTimeout,
		writeTimeout: *reqWriteTimeout,
		dialTimeout:  *dialTimeout,

		socks5Proxy: *socks5,
		contentType: *contentType,
		host:        *host,

		upload:    *upload,
		basicAuth: *basicAuth,
	}

	requester, err := NewRequester(*concurrency, *verbose, *requests, *duration, &clientOpt)
	if err != nil {
		errAndExit(err.Error())
		return
	}

	requester.logf = logf
	requester.think = think

	var ln net.Listener
	// description
	desc := fmt.Sprintf("Benchmarking %s", *urlAddr)
	if *requests > 0 {
		desc += fmt.Sprintf(" with %d request(s)", *requests)
	}
	if *duration > 0 {
		desc += fmt.Sprintf(" for %s", *duration)
	}
	desc += fmt.Sprintf(" using %d connection(s).", *concurrency)

	onlyResultJson := *reportType == "json"

	if !onlyResultJson {
		fmt.Println(desc)

		// charts listener
		if *port > 0 && *requests != 1 {
			*port = getFreePort(*port)
		}

		if *port > 0 && *requests != 1 {
			addr := fmt.Sprintf(":%d", *port)
			if ln, err = net.Listen("tcp", addr); err != nil {
				errAndExit(err.Error())
			}
			fmt.Printf("@ Real-time charts is listening on http://%s\n", ln.Addr().String())
		}
		fmt.Printf("\n")
	}

	// do request
	go requester.Run()

	// metrics collection
	report := NewStreamReport(*concurrency, *verbose)
	go report.Collect(requester.RecordChan())

	if ln != nil {
		// serve charts data
		charts, err := NewCharts(ln, report.Charts, desc)
		if err != nil {
			errAndExit(err.Error())
		}
		go charts.Serve(*autoOpenBrowser)
	}

	// terminal printer
	p := &Printer{maxNum: *requests, maxDuration: *duration, verbose: *verbose, desc: desc, upload: requester.upload}
	p.PrintLoop(report.Snapshot, 200*time.Millisecond, false, onlyResultJson, report.Done(), *requests, logf)
}

func createLogFile() *os.File {
	if *verbose < 2 {
		return nil
	}

	t := time.Now().Format(`20060102150405`)
	f, err := os.CreateTemp(".", "blow_detail_"+t+"_"+"*.log")
	if err == nil {
		fmt.Printf("Log details to: %s\n", f.Name())
		return f
	}

	errAndExit(err.Error())
	return nil
}

func getFreePort(port int) int {
	for i := 0; i < 100; i++ {
		if IsPortFree(port) {
			return port
		}
		port++
	}

	return 0
}

func parseBodyArg() (bodyFile string, bodyBytes []byte) {
	if strings.HasPrefix(*body, "@") {
		fileName := (*body)[1:]
		if _, err := os.Stat(fileName); err != nil {
			errAndExit(err.Error())
		}
		if *stream {
			return fileName, nil
		}

		data, err := ioutil.ReadFile(fileName)
		if err != nil {
			errAndExit(err.Error())
		}
		return "", data
	}

	if *body != "" {
		if _, err := os.Stat(*body); err == nil {
			fileName := *body
			if *stream {
				return fileName, nil
			}

			if data, err := ioutil.ReadFile(fileName); err == nil {
				return "", data
			}
		}
	}

	return "", []byte(*body)
}

// IsPortFree tells whether the port is free or not
func IsPortFree(port int) bool {
	l, err := ListenPort(port)
	if err != nil {
		return false
	}

	_ = l.Close()
	return true
}

// ListenPort listens on port
func ListenPort(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf(":%d", port))
}

var reScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+-.]*://`)

const defaultScheme, defaultHost = "http", "127.0.0.1"

func FixURI(uri string) (string, error) {
	if uri == ":" {
		uri = ":80"
	}

	// ex) :8080/hello or /hello or :
	if strings.HasPrefix(uri, ":") || strings.HasPrefix(uri, "/") {
		uri = defaultHost + uri
	}

	// ex) example.com/hello
	if !reScheme.MatchString(uri) {
		uri = defaultScheme + "://" + uri
	}

	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	u.Host = strings.TrimSuffix(u.Host, ":")
	if u.Path == "" {
		u.Path = "/"
	}

	return u.String(), nil
}
