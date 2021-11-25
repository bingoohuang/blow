package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/bingoohuang/blow/profile"
	"github.com/bingoohuang/blow/util"
	"github.com/bingoohuang/gg/pkg/filex"
	"github.com/bingoohuang/gg/pkg/ss"

	"gopkg.in/alecthomas/kingpin.v3-unstable"

	"github.com/bingoohuang/gg/pkg/thinktime"
)

var (
	flag = kingpin.Flag

	concurrency = flag("concurrency", "#connections to run concurrently").Short('c').Default("100").Int()
	requests    = flag("requests", "#requests to run").Short('n').Default("0").Int64()
	duration    = flag("duration", "Duration of test, examples: -d10s -d3m").Short('d').PlaceHolder("DURATION").Duration()
	verbose     = flag("verbose", "v: Show connections in summary. vv: Log requests and response details to file").Short('v').Counter()
	thinkTime   = flag("think", "Think time among requests, eg. 1s, 10ms, 10-20ms and etc. (unit ns, us/µs, ms, s, m, h)").PlaceHolder("DURATION").String()

	body        = flag("body", "HTTP request body, or @file to read from").Short('b').String()
	upload      = flag("upload", "HTTP upload multipart form file or directory, or add prefix file: to set form field name ").Short('u').String()
	qps         = flag("qps", "Rate limit, in queries per second per worker. Default is no rate limit").Short('q').Float64()
	stream      = flag("stream", "Specify whether to stream file specified by '--body @file' using chunked encoding or to read into memory").Default("false").Bool()
	method      = flag("method", "HTTP method").Short('m').String()
	network     = flag("network", "Network simulation, local: simulates local network, lan: local, wan: wide, bad: bad network, or BPS:latency like 20M:20ms").String()
	headers     = flag("header", "Custom HTTP headers").Short('H').PlaceHolder("K:V").Strings()
	profileArg  = flag("profile", "Profile file, append :new to create a demo profile, or :tag to run only specified profile").Short('P').Strings()
	host        = flag("host", "Host header").String()
	enableGzip  = flag("gzip", "Enabled gzip if gzipped content is less more").Bool()
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
	statusName      = flag("status", "Status name in json, like resultCode").String()

	urlAddr = kingpin.Arg("url", "request url").String()
)

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
		if v, err := util.FixURI(*urlAddr); err != nil {
			util.Exit(err.Error())
		} else {
			*urlAddr = v
		}
	} else if len(*profileArg) == 0 {
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

	think, err := thinktime.ParseThinkTime(*thinkTime)
	if err != nil {
		util.Exit(err.Error())
	}

	bodyFile, bodyBytes := util.ParseBodyArg(*body, *stream)

	clientOpt := ClientOpt{
		url:       *urlAddr,
		method:    *method,
		headers:   *headers,
		bodyBytes: bodyBytes,
		bodyFile:  bodyFile,
		upload:    *upload,

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

		basicAuth: *basicAuth,
		network:   *network,
	}

	profiles := parseProfileArg(*profileArg)
	requester, err := NewRequester(*concurrency, *verbose, *requests, *duration, &clientOpt, *statusName, profiles)
	if err != nil {
		util.Exit(err.Error())
	}

	requester.logf = util.CreateLogFile(*verbose)
	requester.think = think

	var ln net.Listener
	// description
	desc := fmt.Sprintf("Benchmarking %s", ss.Or(*urlAddr, "profiles"))
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
			*port = util.GetFreePortStart(*port)
		}

		if *port > 0 && *requests != 1 && *verbose >= 1 {
			addr := fmt.Sprintf(":%d", *port)
			if ln, err = net.Listen("tcp", addr); err != nil {
				util.Exit(err.Error())
			}
			fmt.Printf("@ Real-time charts is listening on http://%s\n", ln.Addr().String())
		}
		fmt.Printf("\n")
	}

	// do request
	go requester.Run()

	// metrics collection
	report := NewStreamReport()
	go report.Collect(requester.RecordChan())

	if ln != nil {
		// serve charts data
		charts, err := NewCharts(ln, report.Charts, desc)
		if err != nil {
			util.Exit(err.Error())
		}

		go charts.Serve(*port)
	}

	// terminal printer
	p := &Printer{maxNum: *requests, maxDuration: *duration, verbose: *verbose, desc: desc, upload: requester.upload}
	p.PrintLoop(report.Snapshot, 200*time.Millisecond, false, onlyResultJson, report.Done(), *requests, requester.logf)
}

func parseProfileArg(profileArg []string) []*profile.Profile {
	var profiles []*profile.Profile
	hasNew := false
	var tag *util.Tag
	for _, p := range profileArg {
		if strings.HasSuffix(p, ":new") {
			name := p[:len(p)-4]
			util.ExitIfErr(os.WriteFile(name, []byte(profile.DemoProfile), os.ModePerm))
			fmt.Printf("profile file %s created\n", name)
			hasNew = true
			continue
		}

		if tagPos := strings.LastIndex(p, ":"); tagPos > 0 {
			tag = util.ParseTag(p[tagPos+1:])
			p = p[:tagPos]
		}

		if !filex.Exists(p) {
			util.Exit("profile " + p + " doesn't exist")
		}

		pp, err := profile.ParseProfileFile("", p)
		if err != nil {
			util.Exit(err.Error())
		}

		for _, p1 := range pp {
			if tag == nil || tag.Contains(p1.Tag) {
				profiles = append(profiles, p1)
			}
		}
	}
	if hasNew {
		os.Exit(0)
	}
	return profiles
}
