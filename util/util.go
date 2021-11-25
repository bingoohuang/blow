package util

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/valyala/fasthttp"
	"go.uber.org/multierr"
)

func Quoted(s, open, close string) (string, bool) {
	p1 := strings.Index(s, open)
	if p1 != 0 {
		return "", false
	}

	s = s[len(open):]
	if !strings.HasSuffix(s, close) {
		return "", false
	}

	return strings.TrimSuffix(s, close), true
}

func MergeCodes(codes []string) string {
	n := 0
	last := ""
	merged := ""
	for _, code := range codes {
		if code != last {
			if last != "" {
				merged = mergeCodes(merged, n, last)
			}
			last = code
			n = 1
		} else {
			n++
		}
	}

	if n > 0 {
		merged = mergeCodes(merged, n, last)
	}

	return merged
}

func mergeCodes(merged string, n int, last string) string {
	if merged != "" {
		merged += ","
	}
	if n > 1 {
		merged += fmt.Sprintf("%sx%d", last, n)
	} else {
		merged += fmt.Sprintf("%s", last)
	}
	return merged
}

type Closers []io.Closer

func (closers Closers) Close() (err error) {
	for _, c := range closers {
		err = multierr.Append(err, c.Close())
	}

	return
}

// SetHeader set request header if value is not empty.
func SetHeader(r *fasthttp.Request, header, value string) {
	if value != "" {
		r.Header.Set(header, value)
	}
}

func ParseBodyArg(body string, stream bool) (fileName string, bodyBytes []byte) {
	if strings.HasPrefix(body, "@") {
		fileName = (body)[1:]
		if _, err := os.Stat(fileName); err != nil {
			Exit(err.Error())
		}
		if stream {
			return fileName, nil
		}

		data, err := ioutil.ReadFile(fileName)
		if err != nil {
			Exit(err.Error())
		}
		return "", data
	}

	if body != "" {
		if _, err := os.Stat(body); err == nil {
			fileName = body
			if stream {
				return fileName, nil
			}

			if data, err := ioutil.ReadFile(fileName); err == nil {
				return "", data
			}
		}
	}

	return "", []byte(body)
}

func ExitIfErr(err error) {
	if err != nil {
		Exit(err.Error())
	}
}

func Exit(msg string) {
	fmt.Fprintln(os.Stderr, "blow: "+msg)
	os.Exit(1)
}

func GetFreePortStart(port int) int {
	for i := 0; i < 100; i++ {
		if IsPortFree(port) {
			return port
		}
		port++
	}

	return 0
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
