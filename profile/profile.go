package profile

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"io"
	"net/textproto"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/bingoohuang/blow/util"
	"github.com/bingoohuang/gg/pkg/rest"
	"github.com/bingoohuang/jj"
	"github.com/valyala/fasthttp"
)

func (p *Profile) CreateReq(client *fasthttp.HostClient, req *fasthttp.Request, enableGzip bool) (util.Closers, error) {
	p.requestHeader.CopyTo(&req.Header)
	if client.IsTLS {
		req.URI().SetScheme("https")
		req.URI().SetHostBytes(req.Header.Host())
	}

	if enableGzip && p.Header["Accept-Encoding"] != "" {
		req.Header.Set("Accept-Encoding", "gzip")
	}

	if p.bodyFileName != "" {
		file, err := os.Open(p.bodyFileName)
		if err != nil {
			return nil, err
		}
		req.SetBodyStream(file, -1)
		return []io.Closer{file}, nil
	}

	if len(p.bodyFileData) > 0 {
		bodyBytes := p.bodyFileData
		if enableGzip {
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			zw.Write(bodyBytes)
			zw.Close()
			if v := buf.Bytes(); len(v) < len(p.bodyFileData) {
				bodyBytes = v
				req.Header.Set("Content-Encoding", "gzip")
			}
		}

		req.SetBodyRaw(bodyBytes)
		return nil, nil
	}

	for k, v := range p.Form {
		// 先处理，只上传一个文件的情形
		if strings.HasPrefix(v, "@") {
			data, cType, err := util.ReadMultipartFile(true, k, v[1:])
			if err != nil {
				panic(err)
			}
			util.SetHeader(req, "Content-Type", cType)
			req.SetBody(data)
			return nil, nil
		}
	}

	return nil, nil
}

func (p *Profile) createHeader() error {
	u, err := url.Parse(p.URL)
	if err != nil {
		return err
	}

	contentType := p.Header[ContentTypeName]
	if contentType == "" {
		contentType = `plain/text; charset=utf-8`
	} else {
		delete(p.Header, ContentTypeName)
	}

	host := u.Host
	if v := p.Header["Host"]; v != "" {
		host = v
		delete(p.Header, "Host")
	}

	p.requestHeader = &fasthttp.RequestHeader{}
	p.requestHeader.SetHost(host)
	p.requestHeader.SetContentType(contentType)
	p.requestHeader.SetMethod(p.Method)
	u.RawQuery = p.makeQuery(u.Query()).Encode()
	p.requestHeader.SetRequestURI(u.RequestURI())

	if v := p.Header["Basic"]; v != "" {
		b := base64.StdEncoding.EncodeToString([]byte(v))
		p.requestHeader.Set("Authorization", "Basic "+b)
		delete(p.Header, "Basic")
	}

	for k, v := range p.Header {
		p.requestHeader.Set(k, v)
	}

	return nil
}

func (p *Profile) makeQuery(query url.Values) url.Values {
	switch p.Method {
	case "GET", "HEAD", "CONNECT", "OPTIONS", "TRACE":
		for k, v := range p.Form {
			query.Set(k, v)
		}
		for k, v := range p.Query {
			query.Set(k, v)
		}
	}
	return query
}

type Profile struct {
	Method   string
	URL      string
	Query    map[string]string
	RawJSON  map[string]string
	Form     map[string]string
	Header   map[string]string
	Body     string
	Comments []string

	requestHeader *fasthttp.RequestHeader
	bodyFileName  string
	bodyFileData  []byte
}

const DemoProfile = `
###
GET http://127.0.0.1:5003/status

###
POST http://127.0.0.1:5003/dynamic/demo
{"name": "bingoo"}

###
POST http://127.0.0.1:5003/dynamic/demo
{"name": "huang"}

###
POST http://127.0.0.1:5003/dynamic/demo
{"name": "ding", "age": 10}

###
POST http://127.0.0.1:5003/dynamic/demo
{"name": "ding", "age": 20}
`

func ParseProfileFile(baseUrl string, fileName string) ([]*Profile, error) {
	f, err := os.Open(fileName)
	if err != nil {
		panic(err.Error())
	}
	defer f.Close()

	return ParseProfiles(baseUrl, f)
}

func ParseProfiles(baseUrl string, r io.Reader) ([]*Profile, error) {
	buf := bufio.NewReader(r)
	profiles, err := parseRequests(baseUrl, buf)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return profiles, nil
		}

		return nil, err
	}

	return profiles, nil
}

func parseRequests(baseUrl string, buf *bufio.Reader) (profiles []*Profile, err error) {
	var p *Profile
	var l string

	for err == nil || len(l) > 0 {
		if len(l) > 0 {
			p1 := processLine(p, baseUrl, l)
			if p1 != p {
				profiles = append(profiles, p1)
				p = p1
			}
		}

		l, err = buf.ReadString('\n')
		l = strings.TrimSpace(l)
	}

	if err = postProcessProfiles(profiles); err != nil {
		return nil, err
	}

	return
}

func postProcessProfiles(profiles []*Profile) error {
	for _, p := range profiles {
		if len(p.Body) > 0 {
			p.bodyFileName, p.bodyFileData = util.ParseBodyArg(p.Body, false)

			if p.Header[ContentTypeName] == "" && jj.Valid(p.Body) {
				p.Header[ContentTypeName] = ContentTypeJSON
			}
		}

		if err := p.createHeader(); err != nil {
			return err
		}
	}
	return nil
}

const (
	ContentTypeName = "Content-Type"
	ContentTypeJSON = "application/json;charset=utf-8"
)

var headerReg = regexp.MustCompile(`(^\w+(?:-\w+)*)(==|:=|=|:|@)\s*(.*)$`)

var lastComments []string

func processLine(p *Profile, baseUrl, l string) *Profile {
	if m, ok := hasAnyPrefix(strings.ToUpper(l),
		"GET", "HEAD", "POST", "PUT",
		"PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE"); ok {
		addr := strings.TrimSpace(l[len(m):])
		p1 := &Profile{
			Method: m, URL: fixUrl(baseUrl, addr),
			Comments: lastComments,
			Header:   map[string]string{},
			Query:    map[string]string{},
			Form:     map[string]string{},
			RawJSON:  map[string]string{},
		}
		lastComments = lastComments[:0]
		return p1
	}

	if strings.HasPrefix(l, "#") { // 遇到注释了
		lastComments = append(lastComments, l)
		return p
	}

	p.Comments = append(p.Comments, lastComments...)
	lastComments = lastComments[:0]

	if len(p.Body) == 0 {
		if subs := headerReg.FindStringSubmatch(l); len(subs) > 0 {
			k, op, v := subs[1], subs[2], subs[3]
			// refer https://httpie.io/docs#request-items
			switch op {
			case "==": //  query string parameter
				p.Query[k] = v
			case ":=": // Raw JSON fields
				p.RawJSON[k] = v
			case ":": // Header fields
				ck := textproto.CanonicalMIMEHeaderKey(k)
				p.Header[ck] = v
			case "@":
				// File upload fields: field@/dir/file, field@file;type=mime
				// For example: screenshot@~/Pictures/img.png, cv@cv.txt;type=text/markdown
				// the presence of a file field results in a --multipart request
				p.Form[k] = "@" + v
			case "=":
				// Data Fields field=value, field=@file.txt
				// Request data fields to be serialized as a JSON object (default),
				// to be form-encoded (with --form, -f),
				// or to be serialized as multipart/form-data (with --multipart)
				p.Form[k] = v
			}

			return p
		}
	}

	p.Body += l
	return p
}

func hasAnyPrefix(s string, subs ...string) (string, bool) {
	for _, sub := range subs {
		if l := len(sub); len(s) > l && strings.HasPrefix(s, sub) {
			if unicode.IsSpace(rune(s[l])) {
				return sub, true
			}
		}
	}

	return "", false
}

func fixUrl(baseUrl, s string) string {
	if baseUrl != "" {
		return s
	}

	v, err := rest.FixURI(s)
	if err != nil {
		panic(err)
	}

	return v
}
