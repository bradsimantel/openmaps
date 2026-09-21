package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	Endpoint string
	Index    string
	HTTP     *http.Client
}

func NewClient(endpoint, index string) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
		return nil, fmt.Errorf("invalid OpenSearch endpoint")
	}
	if index == "" || strings.ContainsAny(index, `/\\*?"<>| ,#:`) {
		return nil, fmt.Errorf("invalid OpenSearch index")
	}
	return &Client{Endpoint: strings.TrimRight(endpoint, "/"), Index: index, HTTP: &http.Client{Timeout: 35 * time.Second}}, nil
}

func (c *Client) request(ctx context.Context, method, path, contentType string, body []byte, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.Endpoint+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 64<<20)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(limited, 1<<20))
		return response.StatusCode, fmt.Errorf("OpenSearch %s %s: HTTP %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(message)))
	}
	if out != nil {
		decoder := json.NewDecoder(limited)
		if err = decoder.Decode(out); err != nil {
			return response.StatusCode, err
		}
	}
	return response.StatusCode, nil
}

func (c *Client) CreateIndex(ctx context.Context) error {
	_, err := c.request(ctx, http.MethodPut, "/"+url.PathEscape(c.Index), "application/json", IndexDefinition(), nil)
	return err
}

func (c *Client) Ready(ctx context.Context) error {
	var result struct {
		Version struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	if _, err := c.request(ctx, http.MethodGet, "/", "", nil, &result); err != nil {
		return err
	}
	if result.Version.Number == "" {
		return fmt.Errorf("OpenSearch response has no version")
	}
	return nil
}
