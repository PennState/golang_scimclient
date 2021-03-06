package scim

import (
	"errors"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/PennState/httputil/pkg/httperror"
	"github.com/PennState/httputil/pkg/httptest"
	"github.com/stretchr/testify/assert"
)

func TestClientOptsParsing(t *testing.T) {
	type booleanOpt func(bool) ClientOpt
	opts := []booleanOpt{DisableDiscovery, DisableEtag, IgnoreRedirects}
	count := int(math.Pow(2, float64(len(opts))))
	for i := 0; i < count; i++ {
		dd := i&1 != 0
		de := i&2 != 0
		ir := i&4 != 0
		name := fmt.Sprintf("DisableDiscovery: %t, DisableEtag: %t, IgnoreRedirects: %t", dd, de, ir)
		t.Run(name, func(t *testing.T) {
			c, err := NewClient(
				nil,
				"https://example.com/scim",
				DisableDiscovery(dd),
				DisableEtag(de),
				IgnoreRedirects(ir),
			)
			assert.NoError(t, err)
			assert.Equal(t, dd, c.cfg.DisableDiscovery)
			assert.Equal(t, de, c.cfg.DisableEtag)
			assert.Equal(t, ir, c.cfg.IgnoreRedirects)
		})
	}
}

func TestNewClientFromEnv(t *testing.T) {
	url := "https://example.com/scim"
	os.Setenv("SCIM_SERVICE_URL", url)
	os.Setenv("SCIM_IGNORE_REDIRECTS", "true")
	os.Setenv("SCIM_DISABLE_DISCOVERY", "true")
	os.Setenv("SCIM_DISABLE_ETAG", "true")
	c, err := NewClientFromEnv(nil)
	assert.NoError(t, err)
	assert.Equal(t, c.cfg.ServiceURL, url)
	assert.True(t, c.cfg.IgnoreRedirects)
	assert.True(t, c.cfg.DisableDiscovery)
	assert.True(t, c.cfg.DisableEtag)
}

func TestServiceURLParsing(t *testing.T) {
	tests := []struct {
		Name        string
		InputURL    string
		ExpectedURL string
		ErrMessage  string
	}{
		{"Valid URL", "http://example.com/scim", "http://example.com/scim", ""},
		{"Valid URL with trailing slash", "http://example.com/scim/", "http://example.com/scim", ""},
		{"Empty URL", "", "", noServiceURLMessage},
		{"Invalid URL", ":", "", invalidServiceURLMessage},
	}
	for idx := range tests {
		test := tests[idx]
		t.Run(test.Name, func(t *testing.T) {
			c, err := NewClient(nil, test.InputURL)
			if err != nil {
				assert.EqualError(t, err, test.ErrMessage)
				return
			}
			assert.Equal(t, test.ExpectedURL, c.cfg.ServiceURL)
		})
	}
}

func TestError(t *testing.T) {
	const er = `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:Error"],
		"scimType":"mutability",
		"detail":"Attribute 'id' is readOnly",
		"status": "400"
	}`

	tests := []struct {
		name string
		resp *http.Response
		exp  error
	}{
		{
			name: "HTTP error - no body",
			resp: &http.Response{
				StatusCode: 400,
				Status:     "Bad request",
			},
			exp: httperror.HTTPError{
				Code:        400,
				Description: "Bad request",
			},
		},
		{
			name: "HTTP error - with empty body",
			resp: &http.Response{
				StatusCode: 400,
				Status:     "Bad request",
				Body:       ioutil.NopCloser(strings.NewReader("")),
			},
			exp: httperror.HTTPError{
				Code:        400,
				Description: "Bad request",
				Body:        "",
			},
		},
		{
			name: "HTTP error - with body",
			resp: &http.Response{
				StatusCode: 400,
				Status:     "Bad request",
				Body:       ioutil.NopCloser(strings.NewReader("Response body")),
			},
			exp: httperror.HTTPError{
				Code:        400,
				Description: "Bad request",
				Body:        "Response body",
			},
		},
		{
			name: "SCIM ErrorResponse",
			resp: &http.Response{
				StatusCode: 400,
				Status:     "Bad request",
				Body:       ioutil.NopCloser(strings.NewReader(er)),
			},
			exp: ErrorResponse{
				Schemas:  []string{"urn:ietf:params:scim:api:messages:2.0:Error"},
				ScimType: "mutability",
				Detail:   "Attribute 'id' is readOnly",
				Status:   "400",
			},
		},
	}

	for idx := range tests {
		test := tests[idx]
		t.Run(test.name, func(t *testing.T) {
			c := Client{
				client: &client{},
			}
			act := c.error(test.resp)
			assert.Equal(t, test.exp, act)
		})
	}
}

func TestETag(t *testing.T) {
	vers := "W\\/\"3694e05e9dff590\""
	res := User{
		CommonAttributes: CommonAttributes{
			Meta: Meta{
				Version: vers,
			},
		},
	}

	tests := []struct {
		name     string
		disabled bool
	}{
		{"ETags disabled", true},
		{"ETags enabled", false},
	}

	for idx := range tests {
		test := tests[idx]
		t.Run(test.name, func(t *testing.T) {
			c := Client{
				client: &client{
					cfg: &clientCfg{
						DisableEtag: test.disabled,
					},
				},
			}

			req := http.Request{
				Header: map[string][]string{},
			}

			c.etag(&res, &req)

			if test.disabled {
				assert.NotContains(t, req.Header, "If-Match")
				return
			}

			assert.Contains(t, req.Header, "If-Match")
			exp := []string{}
			exp = append(exp, vers)
			assert.Equal(t, exp, req.Header["If-Match"])
		})
	}
}

func TestResourceOrError(t *testing.T) {
	const minuser = `
	{
		"schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
		"id": "2819c223-7f76-453a-919d-413861904646",
		"userName": "bjensen@example.com",
		"meta": {
			"resourceType": "User",
			"created": "2010-01-23T04:56:22Z",
			"lastModified": "2011-05-13T04:42:34Z",
			"version": "W\/\"3694e05e9dff590\"",
			"location": "https://example.com/v2/Users/2819c223-7f76-453a-919d-413861904646"
		}
	}
	`

	tests := []struct {
		name string
		mock httptest.MockTransport
		exp  error
	}{
		{
			name: "Protocol error",
			mock: httptest.MockTransport{
				Req: &http.Request{
					Header: map[string][]string{},
				},
				Err: errors.New("Protocol Error"),
			},
			exp: &url.Error{
				Op:  "Get",
				URL: "",
				Err: errors.New("Protocol Error"),
			},
		},
		{
			name: "HTTP error",
			mock: httptest.MockTransport{
				Req: &http.Request{
					Header: map[string][]string{},
				},
				Resp: &http.Response{
					StatusCode: 400,
					Status:     "Bad request",
				},
			},
			exp: httperror.HTTPError{
				Code:        400,
				Description: "Bad request",
			},
		},
		{
			name: "No body",
			mock: httptest.MockTransport{
				Req: &http.Request{
					Header: map[string][]string{},
				},
				Resp: &http.Response{
					StatusCode: 200,
					Body:       nil,
				},
			},
			exp: errors.New("<No body>"),
		},
		{
			name: "Bad JSON",
			mock: httptest.MockTransport{
				Req: &http.Request{
					Header: map[string][]string{},
				},
				Resp: &http.Response{
					StatusCode: 200,
					Body:       ioutil.NopCloser(strings.NewReader("}")),
				},
			},
			exp: CodecError{
				Err:  "invalid character '}' looking for beginning of value",
				Op:   Unmarshal,
				Body: []byte("}"),
			},
		},
		{
			name: "Correct",
			mock: httptest.MockTransport{
				Req: &http.Request{
					Header: map[string][]string{},
				},
				Resp: &http.Response{
					StatusCode: 200,
					Body:       ioutil.NopCloser(strings.NewReader(minuser)),
				},
			},
		},
	}

	for idx := range tests {
		test := tests[idx]
		t.Run(test.name, func(t *testing.T) {
			test.mock.Req = &http.Request{
				URL:    &url.URL{},
				Header: map[string][]string{},
			}
			cl := Client{
				client: &client{
					http: &http.Client{
						Transport: test.mock,
					},
				},
			}
			user := User{}
			act := cl.resourceOrError(&user, test.mock.Req)

			assert.Contains(t, test.mock.Req.Header, "Accept")
			assert.Contains(t, test.mock.Req.Header, "Content-Type")

			if test.name != "Correct" {
				assert.Error(t, act)
				assert.Equal(t, test.exp, act)
				return
			}

			assert.NoError(t, act)
		})
	}
}
