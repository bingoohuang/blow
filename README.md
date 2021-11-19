# blow

[![build](https://github.com/bingoohuang/blow/actions/workflows/release.yml/badge.svg)](https://github.com/bingoohuang/blow/actions/workflows/release.yml)
[![Homebrew](https://img.shields.io/badge/dynamic/json.svg?url=https://formulae.brew.sh/api/formula/plow.json&query=$.versions.stable&label=homebrew)](https://formulae.brew.sh/formula/plow)
[![GitHub license](https://img.shields.io/github/license/bingoohuang/blow.svg)](https://github.com/bingoohuang/blow/blob/main/LICENSE)
[![made-with-Go](https://img.shields.io/badge/Made%20with-Go-1f425f.svg)](http://golang.org)

Blow is a HTTP(S) benchmarking tool, written in Golang. It uses
excellent [fasthttp](https://github.com/valyala/fasthttp#http-client-comparison-with-nethttp) instead of Go's default
net/http due to its lightning fast performance.

Features:

1. TODO: support [hurl format](https://github.com/Orange-OpenSource/hurl)
2. test with detail request and response printing by `-n1`
3. enable web service by `-v`
4. enable logging for requests and responses details by `-vv`
5. status by response json fields instead of http status code. e.g. `blow :9335 --status status`.
6. network simulating. e.g. `blow :9335 --network 200K` to simulating bandwidth 200KB/s without latency.
7. network simulating. e.g. `blow :9335 --network 20M:500ms` to simulating bandwidth 20M/s and latency 500ms.
8. uploading files in a dir. e.g. `blow :9335 --upload image-dir -n1`
9. uploading single file. e.g. `blow :9335 --upload 1.jpg -n1`
10. uploading big files without cache. e.g. `blow :9335 --upload .:nocache --user scott:tiger -n1`
11. basic auth. e.g. `blow :9335 --user username:password`
12. thinking time among requests. e.g. `blow :8080 --think 100ms`, `blow :8080 --think 100-300ms`
13. QPS to limit requests per second and per worker. e.g. `blow :8080 --qps 1000`
14. help `blow --help`

Blow runs at a specified connections(option `-c`) concurrently and **real-time** records a summary statistics, histogram
of execution time and calculates percentiles to display on Web UI and terminal. It can run for a set duration(
option `-d`), for a fixed number of requests(option `-n`), or until Ctrl-C interrupted.

The implementation of real-time computing Histograms and Quantiles using stream-based algorithms inspired
by [prometheus](https://github.com/prometheus/client_golang) with low memory and CPU bounds. so it's almost no
additional performance overhead for benchmarking.

![](https://github.com/bingoohuang/blow/blob/main/demo.gif?raw=true)

```text
❯ ./blow 8080/hello -c20
Benchmarking http://127.0.0.1:8080/hello using 20 connection(s).
@ Real-time charts is listening on http://[::]:18888

Summary:
  Elapsed        8.6s
  Count        969657
    2xx        776392
    4xx        193265
  RPS      112741.713
  Reads    10.192MB/s
  Writes    6.774MB/s

Statistics    Min       Mean     StdDev      Max
  Latency     32µs      176µs     37µs     1.839ms
  RPS       108558.4  112818.12  2456.63  115949.98

Latency Percentile:
  P50     P75    P90    P95    P99   P99.9  P99.99
  173µs  198µs  222µs  238µs  274µs  352µs  498µs

Latency Histogram:
  141µs  273028  ■■■■■■■■■■■■■■■■■■■■■■■■
  177µs  458955  ■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■■
  209µs  204717  ■■■■■■■■■■■■■■■■■■
  235µs   26146  ■■
  269µs    6029  ■
  320µs     721
  403µs      58
  524µs       3
```

## Installation

`go install github.com/bingoohuang/blow`

## Usage

### Options

```bash
🕙[2021-11-16 22:07:50.899] ❯ blow --help
usage: blow [<flags>] [<url>]

A high-performance HTTP benchmarking tool with real-time web UI and terminal displaying

Examples:

  blow :18888/api/hello
  blow http://127.0.0.1:8080/ -c20 -n100000
  blow https://httpbin.org/post -c20 -d5m --body @file.json -T 'application/json' -m POST

Flags:
      --help                   Show context-sensitive help.
  -c, --concurrency=100        #connections to run concurrently
  -n, --requests=-1            #requests to run
  -d, --duration=DURATION      Duration of test, examples: -d10s -d3m
  -v, --verbose ...            v: Show connections in summary. vv: Log requests and response details to file
      --think=DURATION         Think time among requests, eg. 1s, 10ms, 10-20ms and etc. (unit ns, us/µs, ms, s, m, h)
  -b, --body=BODY              HTTP request body, or @file to read from
  -u, --upload=UPLOAD          HTTP upload multipart form file or directory, or add prefix file: to set form field name
  -q, --qps=QPS                Rate limit, in queries per second per worker. Default is no rate limit
      --stream                 Specify whether to stream file specified by '--body @file' using chunked encoding or to read into memory
  -m, --method=METHOD          HTTP method
      --network=NETWORK        Network simulation, local: simulates local network, lan: local, wan: wide, bad: bad network, or BPS:latency like 20M:20ms
  -H, --header=K:V ...         Custom HTTP headers
      --host=HOST              Host header
      --gzip                   Enabled gzip if gzipped content is less more
      --user=USER              basic auth username:password
  -T, --content=CONTENT        Content-Type header
      --cert=CERT              Path to the client's TLS Certificate
      --key=KEY                Path to the client's TLS Certificate Private Key
  -k, --insecure               Controls whether a client verifies the server's certificate chain and host name
      --report=REPORT          How to report, dynamic (default) / json
      --port=18888             Listen port for serve Web UI
      --timeout=DURATION       Timeout for each http request
      --dial-timeout=DURATION  Timeout for dial addr
      --req-timeout=DURATION   Timeout for full request writing
      --rsp-timeout=DURATION   Timeout for full response reading
      --socks5=ip:port         Socks5 proxy
      --status=STATUS          Status name in json, like resultCode
      --version                Show application version.

  Flags default values also read from env BLOW_SOME_FLAG, such as BLOW_TIMEOUT=5s equals to --timeout=5s

Args:
  [<url>]  request url
```

### Examples

1. Basic usage: `blow http://127.0.0.1:8080/ -c20 -n10000 -d10s`
2. POST a json file:  `blow https://httpbin.org/post -c20 --body @file.json -T 'application/json' -m POST`

## License

See [LICENSE](https://github.com/bingoohuang/blow/blob/master/LICENSE).
