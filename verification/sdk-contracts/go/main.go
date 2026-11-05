package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	ali "github.com/aliyun/credentials-go/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/endpointcreds"
	"io"
	"net"
	"net/http"
	"os"
)

func main() {
	payload, _ := os.ReadFile(os.Getenv("SDK_FIXTURE_FILE"))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			r, e := http.ReadRequest(bufio.NewReader(server))
			if e != nil {
				return
			}
			r.Body.Close()
			response := http.Response{StatusCode: 200, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload)), Close: true}
			response.Write(server)
		}()
		return client, nil
	}
	http.DefaultTransport = transport
	result := map[string]any{"resolved": false, "complete": false}
	vendor, mode, target := os.Args[1], os.Args[2], os.Args[3]
	var id, secret, token string
	var err error
	if vendor == "alibaba" {
		var cfg *ali.Config
		if mode == "explicit" {
			cfg = new(ali.Config).SetType("credentials_uri").SetURLCredential(target)
		}
		c, e := ali.NewCredential(cfg)
		if e != nil {
			err = e
		} else {
			cred, e := c.GetCredential()
			err = e
			if e == nil && cred != nil {
				if cred.AccessKeyId != nil {
					id = *cred.AccessKeyId
				}
				if cred.AccessKeySecret != nil {
					secret = *cred.AccessKeySecret
				}
				if cred.SecurityToken != nil {
					token = *cred.SecurityToken
				}
			}
		}
	} else {
		c, e := endpointcreds.New(target, func(o *endpointcreds.Options) { o.HTTPClient = &http.Client{Transport: fixtureTransport(payload)} }).Retrieve(context.Background())
		err = e
		id, secret, token = c.AccessKeyID, c.SecretAccessKey, c.SessionToken
	}
	if err == nil {
		result["resolved"] = true
		result["complete"] = id == "FAKE_ID" && secret == "FAKE_SECRET" && token == "FAKE_TOKEN"
	} else {
		result["errorClass"] = "sdk-rejected"
	}
	json.NewEncoder(os.Stdout).Encode(result)
}

type fixtureTransport []byte

func (f fixtureTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(f)), Header: make(http.Header)}, nil
}
