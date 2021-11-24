package profile

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseProfiles(t *testing.T) {
	const s = `
###
GET http://127.0.0.1:1080

###
POST http://127.0.0.1:1080
Bingoo-Name: bingoohuang
{"name": "bingoohuang", "age": 1000}
`
	profiles, err := ParseProfiles("", strings.NewReader(s))
	assert.Nil(t, err)

	for _, p := range profiles {
		p.requestHeader = nil
		p.bodyFileName = ""
		p.bodyFileData = nil
	}

	assert.Equal(t, []*Profile{
		{
			Comments: []string{"###"}, Method: "GET", URL: "http://127.0.0.1:1080/",
			Header:  map[string]string{},
			Form:    map[string]string{},
			RawJSON: map[string]string{},
			Query:   map[string]string{},
		},
		{
			Comments: []string{"###"}, Method: "POST", URL: "http://127.0.0.1:1080/",
			Header:  map[string]string{"Bingoo-Name": "bingoohuang"},
			Form:    map[string]string{},
			RawJSON: map[string]string{},
			Query:   map[string]string{},
			Body:    `{"name": "bingoohuang", "age": 1000}`,
		},
	}, profiles)
}
