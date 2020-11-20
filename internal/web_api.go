package internal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/livechat/lc-sdk-go/v2/authorization"
	api_errors "github.com/livechat/lc-sdk-go/v2/errors"
)

const apiVersion = "3.3"

type api struct {
	httpClient           *http.Client
	clientID             string
	tokenGetter          authorization.TokenGetter
	httpRequestGenerator HTTPRequestGenerator
	host                 string
	customHeaders        http.Header
}

// HTTPRequestGenerator is called by each API method to generate api http url.
type HTTPRequestGenerator func(*authorization.Token, string, string) (*http.Request, error)

// NewAPI returns ready to use raw API client. This is a base that is used internally
// by specialized clients for each API, you should use those instead
//
// If provided client is nil, then default http client with 20s timeout is used.
func NewAPI(t authorization.TokenGetter, client *http.Client, clientID string, r HTTPRequestGenerator) (*api, error) {
	if t == nil {
		return nil, errors.New("cannot initialize api without TokenGetter")
	}

	if client == nil {
		client = &http.Client{
			Timeout: 20 * time.Second,
		}
	}

	return &api{
		tokenGetter:          t,
		clientID:             clientID,
		httpClient:           client,
		host:                 "https://api.livechatinc.com",
		httpRequestGenerator: r,
		customHeaders:        make(http.Header),
	}, nil
}

// Call sends request to API with given action
func (a *api) Call(action string, reqPayload interface{}, respPayload interface{}) error {
	rawBody, err := json.Marshal(reqPayload)
	if err != nil {
		return err
	}
	token := a.tokenGetter()
	if token == nil {
		return fmt.Errorf("couldn't get token")
	}
	if token.Type != authorization.BearerToken && token.Type != authorization.BasicToken {
		return fmt.Errorf("unsupported token type")
	}

	req, err := a.httpRequestGenerator(token, a.host, action)
	if err != nil {
		return fmt.Errorf("couldn't create new http request: %v", err)
	}
	req.Body = ioutil.NopCloser(bytes.NewReader(rawBody))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("%s %s", token.Type, token.AccessToken))
	req.Header.Set("User-agent", fmt.Sprintf("GO SDK Application %s", a.clientID))
	req.Header.Set("X-Region", token.Region)

	for key, val := range a.customHeaders {
		if len(val) == 0 {
			continue
		}
		req.Header.Set(key, val[0])
	}

	return a.send(req, respPayload)
}

// SetCustomHeader allows to set a custom header (e.g. X-Debug-Id or X-Author-Id) that will be sent in every request
func (a *api) SetCustomHeader(key, val string) {
	a.customHeaders.Set(key, val)
}

type fileUploadAPI struct{ *api }

// NewAPIWithFileUpload returns ready to use raw API client with file upload functionality.
func NewAPIWithFileUpload(t authorization.TokenGetter, client *http.Client, clientID string, r HTTPRequestGenerator) (*fileUploadAPI, error) {
	api, err := NewAPI(t, client, clientID, r)
	if err != nil {
		return nil, err
	}
	return &fileUploadAPI{api}, nil
}

// UploadFile uploads a file to LiveChat CDN.
// Returned URL shall be used in call to SendFile or SendEvent or it'll become invalid
// in about 24 hours.
func (a *fileUploadAPI) UploadFile(filename string, file []byte) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	w, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("couldn't create form file: %v", err)
	}
	if _, err := w.Write(file); err != nil {
		return "", fmt.Errorf("couldn't write file to multipart writer: %v", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("couldn't close multipart writer: %v", err)
	}
	token := a.tokenGetter()
	if token == nil {
		return "", fmt.Errorf("couldn't get token")
	}

	req, err := a.httpRequestGenerator(token, a.host, "upload_file")
	if err != nil {
		return "", fmt.Errorf("couldn't create new http request: %v", err)
	}
	req.Method = "POST"
	req.Body = ioutil.NopCloser(body)

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.AccessToken))
	req.Header.Set("User-agent", fmt.Sprintf("GO SDK Application %s", a.clientID))
	req.Header.Set("X-Region", token.Region)

	var resp struct {
		URL string `json:"url"`
	}
	err = a.send(req, &resp)
	return resp.URL, err
}

func (a *api) send(req *http.Request, respPayload interface{}) error {
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		apiErr := &api_errors.ErrAPI{}
		if err := json.Unmarshal(bodyBytes, apiErr); err != nil {
			return fmt.Errorf("couldn't unmarshal error response: %s (code: %d, raw body: %s)", err.Error(), resp.StatusCode, string(bodyBytes))
		}
		if apiErr.Error() == "" {
			return fmt.Errorf("couldn't unmarshal error response (code: %d, raw body: %s)", resp.StatusCode, string(bodyBytes))
		}
		return apiErr
	}

	if err != nil {
		return err
	}

	return json.Unmarshal(bodyBytes, respPayload)
}

// SetCustomHost allows to change API host address. This method is mostly for LiveChat internal testing and should not be used in production environments.
func (a *api) SetCustomHost(host string) {
	a.host = host
}

// DefaultHTTPRequestGenerator generates API request for given service in stable version.
func DefaultHTTPRequestGenerator(name string) HTTPRequestGenerator {
	return func(token *authorization.Token, host, action string) (*http.Request, error) {
		url := fmt.Sprintf("%s/v%s/%s/action/%s", host, apiVersion, name, action)
		return http.NewRequest("POST", url, nil)
	}
}
