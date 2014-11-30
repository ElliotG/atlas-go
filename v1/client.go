package atlas

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

const atlasURL = "https://atlas.hashicorp.com"

// If this is set to true, verbose debug data will be output
var Debug = false

// ErrAuth is the error returned if a 401 is returned by an API request.
var ErrAuth = errors.New("authentication failed")

// ErrNotFound is the error returned if a 404 is returned by an API request.
var ErrNotFound = errors.New("resource not found")

// RailsError represents an error that was returned from the Rails server.
type RailsError struct {
	Errors map[string][]string `json:"errors"`
}

// Error collects all of the errors in the RailsError and returns a comma-
// separated list of the errors that were returned from the server.
func (re *RailsError) Error() string {
	list := make([]string, 0)
	for key, errors := range re.Errors {
		for _, err := range errors {
			list = append(list, fmt.Sprintf("%s: %s", key, err))
		}
	}

	return strings.Join(list, ", ")
}

// Client represents a single connection to a Atlas API endpoint.
type Client struct {
	// URL is the full endpoint address to the Atlas server including the
	// protocol, port, and path.
	URL *url.URL

	// Token is the Atlas authentication token
	Token string

	// HTTPClient is the underlying http client with which to make requests.
	HTTPClient *http.Client
}

// DefaultClient returns a client that connects to the Atlas API.
func DefaultClient() *Client {
	client, err := NewClient(atlasURL)
	if err != nil {
		panic(err)
	}

	return client
}

// NewClient creates a new Atlas Client from the given URL (as a string). If
// the URL cannot be parsed, an error is returned. The HTTPClient is set to
// http.DefaultClient, but this can be changed programatically by setting
// client.HTTPClient. The user can also programtically set the URL as a
// *url.URL.
func NewClient(urlString string) (*Client, error) {
	if len(urlString) == 0 {
		return nil, fmt.Errorf("client: missing url")
	}

	parsedURL, err := url.Parse(urlString)
	if err != nil {
		return nil, err
	}

	client := &Client{
		URL:   parsedURL,
		Token: os.Getenv("ATLAS_TOKEN"),
	}

	if err := client.init(); err != nil {
		return nil, err
	}

	return client, nil
}

// Login accepts a username and password as string arguments. Both username and
// password must be non-nil, non-empty values. Atlas does not permit
// passwordless authentication.
//
// If authentication is unsuccessful, an error is returned with the body of the
// error containing the server's response.
//
// If authentication is successful, this method sets the Token value on the
// Client and returns the Token as a string.
func (c *Client) Login(username, password string) (string, error) {
	if len(username) == 0 {
		return "", fmt.Errorf("client: missing username")
	}

	if len(password) == 0 {
		return "", fmt.Errorf("client: missing password")
	}

	// Make a request
	request, err := c.Request("POST", "/api/v1/authenticate", &RequestOptions{
		Body: strings.NewReader(url.Values{
			"user[login]":       []string{username},
			"user[password]":    []string{password},
			"user[description]": []string{"Created by the Atlas Go Client"},
		}.Encode()),
		Headers: map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
		},
	})
	if err != nil {
		return "", err
	}

	// Make the request
	response, err := checkResp(c.HTTPClient.Do(request))
	if err != nil {
		return "", err
	}

	// Decode the body
	var tokenResponse struct{ Token string }
	if err := decodeJSON(response, &tokenResponse); err != nil {
		return "", nil
	}

	// Set the token
	c.Token = tokenResponse.Token

	// Return the token
	return c.Token, nil
}

// init() sets defaults on the client.
func (c *Client) init() error {
	c.HTTPClient = http.DefaultClient
	return nil
}

//
type RequestOptions struct {
	Params     map[string]string
	Headers    map[string]string
	Body       io.Reader
	BodyLength int64
}

// Request creates a new HTTP request using the given verb and sub path.
func (c *Client) Request(verb, spath string, ro *RequestOptions) (*http.Request, error) {
	// Ensure we have a RequestOptions struct (passing nil is an acceptable)
	if ro == nil {
		ro = new(RequestOptions)
	}

	// Create a new URL with the appended path
	u := *c.URL
	u.Path = path.Join(c.URL.Path, spath)

	// Add the token and other params
	if c.Token != "" {
		if ro.Params == nil {
			ro.Params = make(map[string]string)
		}

		ro.Params["access_token"] = c.Token
	}

	return c.rawRequest(verb, &u, ro)
}

func (c *Client) putFile(rawUrl string, r io.Reader, size int64) error {
	url, err := url.Parse(rawUrl)
	if err != nil {
		return err
	}

	request, err := c.rawRequest("PUT", url, &RequestOptions{
		Body:       r,
		BodyLength: size,
	})
	if err != nil {
		return err
	}

	if _, err := checkResp(c.HTTPClient.Do(request)); err != nil {
		return err
	}

	return nil
}

// rawRequest accepts a verb, URL, and RequestOptions struct and returns the
// constructed http.Request and any errors that occurred
func (c *Client) rawRequest(verb string, u *url.URL, ro *RequestOptions) (*http.Request, error) {
	if verb == "" {
		return nil, fmt.Errorf("client: missing verb")
	}

	if u == nil {
		return nil, fmt.Errorf("client: missing URL.url")
	}

	if ro == nil {
		return nil, fmt.Errorf("client: missing RequestOptions")
	}

	// Add the token and other params
	var params = make(url.Values)
	for k, v := range ro.Params {
		params.Add(k, v)
	}
	u.RawQuery = params.Encode()

	// Create the request object
	request, err := http.NewRequest(verb, u.String(), ro.Body)
	if err != nil {
		return nil, err
	}

	// Add any headers
	for k, v := range ro.Headers {
		request.Header.Add(k, v)
	}

	// Add content-length if we have it
	if ro.BodyLength > 0 {
		request.ContentLength = ro.BodyLength
	}

	return request, nil
}

// checkResp wraps http.Client.Do() and verifies that the request was
// successful. A non-200 request returns an error formatted to included any
// validation problems or otherwise.
func checkResp(resp *http.Response, err error) (*http.Response, error) {
	// If the err is already there, there was an error higher
	// up the chain, so just return that
	if err != nil {
		return resp, err
	}

	switch resp.StatusCode {
	case 200:
		return resp, nil
	case 201:
		return resp, nil
	case 202:
		return resp, nil
	case 204:
		return resp, nil
	case 400:
		return nil, parseErr(resp)
	case 401:
		return nil, ErrAuth
	case 404:
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("client: %s", resp.Status)
	}
}

// parseErr is used to take an error json response and return a single string
// for use in error messages.
func parseErr(resp *http.Response) error {
	railsError := &RailsError{}

	if err := decodeJSON(resp, &railsError); err != nil {
		return fmt.Errorf("Error parsing error body: %s", err)
	}

	return railsError
}

// decodeJSON is used to JSON decode a body into an interface.
func decodeJSON(resp *http.Response, out interface{}) error {
	defer resp.Body.Close()

	var r io.Reader = resp.Body
	if Debug {
		var buf bytes.Buffer
		r = io.TeeReader(resp.Body, &buf)
		defer func() {
			log.Printf("[DEBUG] client: decoding: %s", buf.String())
		}()
	}

	dec := json.NewDecoder(r)
	return dec.Decode(out)
}