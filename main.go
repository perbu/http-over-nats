package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
)

type NATSHTTPTransport struct {
	nc          *nats.Conn
	subjectReq  string
	subjectResp string
	timeout     time.Duration
}

type NATSHTTPRequest struct {
	Method string            `json:"method"`
	URL    string            `json:"url"`
	Header map[string]string `json:"header"`
	Body   []byte            `json:"body"`
}

type NATSHTTPResponse struct {
	StatusCode int               `json:"statusCode"`
	Header     map[string]string `json:"header"`
	Body       []byte            `json:"body"`
}

func NewNATSHTTPTransport(nc *nats.Conn, subjectReq, subjectResp string, timeout time.Duration) *NATSHTTPTransport {
	return &NATSHTTPTransport{
		nc:          nc,
		subjectReq:  subjectReq,
		subjectResp: subjectResp,
		timeout:     timeout,
	}
}

func (t *NATSHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Serialize the HTTP request
	headers := make(map[string]string)
	for key, values := range req.Header {
		headers[key] = values[0]
	}

	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
	}
	natsReq := NATSHTTPRequest{
		Method: req.Method,
		URL:    req.URL.String(),
		Header: headers,
		Body:   body,
	}

	natsReqData, err := json.Marshal(natsReq)
	if err != nil {
		return nil, err
	}

	// Send the request over NATS
	msg, err := t.nc.Request(t.subjectReq, natsReqData, t.timeout)
	if err != nil {
		return nil, err
	}

	// Deserialize the response
	var natsResp NATSHTTPResponse
	if err := json.Unmarshal(msg.Data, &natsResp); err != nil {
		return nil, err
	}

	// Construct the HTTP response
	headersResp := http.Header{}
	for key, value := range natsResp.Header {
		headersResp.Set(key, value)
	}

	return &http.Response{
		StatusCode: natsResp.StatusCode,
		Header:     headersResp,
		Body:       io.NopCloser(bytes.NewReader(natsResp.Body)),
	}, nil
}

func startServer(nc *nats.Conn, subjectReq string) {
	_, err := nc.Subscribe(subjectReq, func(msg *nats.Msg) {
		// Deserialize the incoming NATS request
		var natsReq NATSHTTPRequest
		if err := json.Unmarshal(msg.Data, &natsReq); err != nil {
			nc.Publish(msg.Reply, []byte(`{"error": "invalid request"}`))
			return
		}

		// Make the HTTP request
		httpReq, err := http.NewRequest(natsReq.Method, natsReq.URL, bytes.NewReader(natsReq.Body))
		if err != nil {
			return
		}
		for key, value := range natsReq.Header {
			httpReq.Header.Set(key, value)
		}

		client := &http.Client{}
		resp, err := client.Do(httpReq)
		if err != nil {
			if err := nc.Publish(msg.Reply, []byte(`{"error": "failed to make request"}`)); err != nil {
				panic(err)
			}
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		respHeaders := make(map[string]string)
		for key, values := range resp.Header {
			respHeaders[key] = values[0]
		}

		// Serialize and send the response
		natsResp := NATSHTTPResponse{
			StatusCode: resp.StatusCode,
			Header:     respHeaders,
			Body:       body,
		}
		respData, _ := json.Marshal(natsResp)
		if err := nc.Publish(msg.Reply, respData); err != nil {
			panic(err)
		}
	})
	if err != nil {
		panic(err)
	}
}

func main() {
	nc, _ := nats.Connect(nats.DefaultURL)
	subjectReq := "http.request"
	subjectResp := "http.response"

	// Start the server
	go startServer(nc, subjectReq)

	// Create the HTTP client with NATS transport
	client := &http.Client{
		Transport: NewNATSHTTPTransport(nc, subjectReq, subjectResp, 5*time.Second),
	}

	// Example HTTP request
	resp, err := client.Get("https://example.com")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	println(string(body))
}
