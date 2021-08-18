package main

import (
	"fmt"
	"gopkg.in/alecthomas/kingpin.v3-unstable"
	"io/ioutil"
	"net"
	"os"
	"strings"
)

var (
	flag = kingpin.Flag

	concurrency = flag("concurrency", "Number of connections to run concurrently").Short('c').Default("100").Int()
	requests    = flag("requests", "Number of requests to run").Short('n').Default("-1").Int64()
	duration    = flag("duration", "Duration of test, examples: -d 10s -d 3m").Short('d').PlaceHolder("DURATION").Duration()
	interval    = flag("interval", "Print snapshot result every interval, use 0 to print once at the end").Short('i').Default("200ms").Duration()
	verbose     = flag("verbose", "Verbose to log file of requests and response details").Short('V').Bool()
	thinkTime   = flag("think", "Think time among requests, eg. 1s, 10ms, 10-20ms and etc. (unit ns, us/µs, ms, s, m, h)").PlaceHolder("DURATION").String()

	body        = flag("body", "HTTP request body, if start the body with @, the rest should be a filename to read").Short('b').String()
	stream      = flag("stream", "Specify whether to stream file specified by '--body @file' using chunked encoding or to read into memory").Default("false").Bool()
	method      = flag("method", "HTTP method").Short('m').String()
	headers     = flag("header", "Custom HTTP headers").Short('H').PlaceHolder("K:V").Strings()
	host        = flag("host", "Host header").String()
	contentType = flag("content", "Content-Type header").Short('T').String()
	cert        = flag("cert", "Path to the client's TLS Certificate").ExistingFile()
	key         = flag("key", "Path to the client's TLS Certificate Private Key").ExistingFile()
	insecure    = flag("insecure", "Controls whether a client verifies the server's certificate chain and host name").Short('k').Bool()

	port            = flag("port", "Listen port for serve Web UI").Default("18888").Int()
	timeout         = flag("timeout", "Timeout for each http request").PlaceHolder("DURATION").Duration()
	dialTimeout     = flag("dial-timeout", "Timeout for dial addr").PlaceHolder("DURATION").Duration()
	reqWriteTimeout = flag("req-timeout", "Timeout for full request writing").PlaceHolder("DURATION").Duration()
	rspReadTimeout  = flag("rsp-timeout", "Timeout for full response reading").PlaceHolder("DURATION").Duration()
	socks5          = flag("socks5", "Socks5 proxy").PlaceHolder("ip:port").String()

	autoOpenBrowser = flag("auto-open-browser", "Specify whether auto open browser to show Web charts").Bool()

	url = kingpin.Arg("url", "request url").Required().String()
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

  blow http://127.0.0.1:8080/ -c 20 -n 100000
  blow https://httpbin.org/post -c 20 -d 5m --body @file.json -T 'application/json' -m POST

{{if .Context.Flags -}}
{{T "Flags:"}}
{{.Context.Flags|FlagsToTwoColumns|FormatTwoColumns}}
  Flags default values also read from env PLOW_SOME_FLAG, such as PLOW_TIMEOUT=5s equals to --timeout=5s

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
		Version("1.1.0").
		Author("six-ddc@github").
		Resolver(kingpin.PrefixedEnvarResolver("PLOW_", ";")).
		Help = `A high-performance HTTP benchmarking tool with real-time web UI and terminal displaying`
	kingpin.Parse()

	if *requests >= 0 && *requests < int64(*concurrency) {
		*concurrency = int(*requests)
	}

	if *cert == "" || *key == "" {
		*cert = ""
		*key = ""
	}

	var logf *os.File
	if *verbose || *requests == 1 {
		if tmpFile, err := os.CreateTemp(os.TempDir(), "blowlog.*.log"); err == nil {
			fmt.Printf("Log details to: %s\n", tmpFile.Name())
			logf = tmpFile
		} else {
			errAndExit(err.Error())
		}
	}

	think, err := ParseThinkTime(*thinkTime)
	if err != nil {
		errAndExit(err.Error())
	}

	bodyFile, bodyBytes := parseBodyArg()

	clientOpt := ClientOpt{
		url:       *url,
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
	}

	requester, err := NewRequester(*concurrency, *requests, logf, *duration, &clientOpt, think)
	if err != nil {
		errAndExit(err.Error())
		return
	}

	// description
	desc := fmt.Sprintf("Benchmarking %s", *url)
	if *requests > 0 {
		desc += fmt.Sprintf(" with %d request(s)", *requests)
	}
	if *duration > 0 {
		desc += fmt.Sprintf(" for %s", *duration)
	}
	desc += fmt.Sprintf(" using %d connection(s).", *concurrency)
	fmt.Println(desc)

	// charts listener
	var ln net.Listener
	if *port > 0 {
		*port = getFreePort(*port)
	}

	if *port > 0 {
		addr := fmt.Sprintf(":%d", *port)
		if ln, err = net.Listen("tcp", addr); err != nil {
			errAndExit(err.Error())
		}
		fmt.Printf("@ Real-time charts is listening on http://%s\n", ln.Addr().String())
	}
	fmt.Printf("\n")

	// do request
	go requester.Run()

	// metrics collection
	report := NewStreamReport(*concurrency)
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
	printer := NewPrinter(*requests, *duration)
	printer.PrintLoop(report.Snapshot, *interval, true, report.Done(), *requests, logf)
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
