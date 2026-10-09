// Package httpx performs HTTP requests, logging them in the same format as Python's httpx.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/joe-p/algokit-cli-go/internal/log"
)

var logger = log.New("httpx")

// DefaultTimeout matches httpx's default timeout.
const DefaultTimeout = 5 * time.Second

// Client is the HTTP client used for all requests; replaceable for testing.
var Client = &http.Client{}

// Response is a fully read HTTP response.
type Response struct {
	StatusCode int
	Body       []byte
}

// OK reports whether the status code is 2xx (equivalent to httpx's Response.is_success).
func (r *Response) OK() bool { return r.StatusCode >= 200 && r.StatusCode < 300 }

// JSON decodes the body into v, keeping numbers as json.Number so they print as they were received.
func (r *Response) JSON(v any) error {
	decoder := json.NewDecoder(bytes.NewReader(r.Body))
	decoder.UseNumber()
	return decoder.Decode(v)
}

// Get performs an HTTP GET, logging the request at debug level. Returns an error on connection errors/timeouts.
func Get(url string, headers map[string]string, timeout time.Duration) (*Response, error) {
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	logger.Debug(fmt.Sprintf(`HTTP Request: GET %s "%s %s"`, url, resp.Proto, resp.Status))
	return &Response{StatusCode: resp.StatusCode, Body: body}, nil
}
