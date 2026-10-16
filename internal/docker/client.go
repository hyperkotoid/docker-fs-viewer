package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/denis/docker-fs-viewer/internal/model"
)

type Client struct {
	http       *http.Client
	apiVersion string
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("docker API returned %d: %s", e.Status, e.Message)
}

type ContainerSummary struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	Image  string   `json:"Image"`
	State  string   `json:"State"`
	Status string   `json:"Status"`
}

func New(ctx context.Context) (*Client, error) {
	socket, err := socketPath()
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "unix", socket)
		},
	}
	c := &Client{http: &http.Client{Transport: transport}}
	var version struct {
		APIVersion string `json:"ApiVersion"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/version", nil, &version); err != nil {
		return nil, fmt.Errorf("connect to Docker Desktop at %s: %w", socket, err)
	}
	if version.APIVersion == "" {
		return nil, errors.New("Docker Engine did not report an API version")
	}
	c.apiVersion = version.APIVersion

	var info struct {
		OperatingSystem string `json:"OperatingSystem"`
		Name            string `json:"Name"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/info", nil, &info); err != nil {
		return nil, err
	}
	if !strings.Contains(strings.ToLower(info.OperatingSystem+" "+info.Name), "docker desktop") {
		return nil, fmt.Errorf("unsupported Docker engine %q: this MVP requires Docker Desktop", info.OperatingSystem)
	}
	return c, nil
}

func socketPath() (string, error) {
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		if strings.HasPrefix(host, "unix://") {
			return strings.TrimPrefix(host, "unix://"), nil
		}
		return "", fmt.Errorf("unsupported DOCKER_HOST %q; only a local unix socket is supported", host)
	}
	candidates := []string{"/var/run/docker.sock"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".docker", "run", "docker.sock"))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("Docker socket not found; start Docker Desktop")
}

func (c *Client) List(ctx context.Context) ([]ContainerSummary, error) {
	var containers []ContainerSummary
	err := c.doJSON(ctx, http.MethodGet, "/containers/json?all=1", nil, &containers)
	return containers, err
}

func (c *Client) Inspect(ctx context.Context, id string) (*model.Inspect, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.doJSON(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json?size=true", nil, &raw); err != nil {
		return nil, nil, err
	}
	var inspect model.Inspect
	if err := json.Unmarshal(raw, &inspect); err != nil {
		return nil, nil, fmt.Errorf("decode container inspect: %w", err)
	}
	return &inspect, raw, nil
}

func (c *Client) Export(ctx context.Context, id string) (io.ReadCloser, error) {
	resp, err := c.request(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/export", nil, "")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) Import(ctx context.Context, tarPath, repository, tag string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	q := url.Values{"fromSrc": {"-"}, "repo": {repository}, "tag": {tag}}
	path := "/images/create?" + q.Encode()
	resp, err := c.requestWithLength(ctx, http.MethodPost, path, f, "application/x-tar", stat.Size())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	decoder := json.NewDecoder(resp.Body)
	for {
		var event struct {
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return fmt.Errorf("decode image import response: %w", err)
		}
		if event.ErrorDetail.Message != "" {
			return fmt.Errorf("Docker image import failed: %s", event.ErrorDetail.Message)
		}
		if event.Error != "" {
			return fmt.Errorf("Docker image import failed: %s", event.Error)
		}
	}
}

func (c *Client) Rename(ctx context.Context, id, name string) error {
	q := url.Values{"name": {name}}
	return c.doJSON(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/rename?"+q.Encode(), nil, nil)
}

func (c *Client) Create(ctx context.Context, name string, body any) (string, error) {
	var response struct {
		ID       string   `json:"Id"`
		Warnings []string `json:"Warnings"`
	}
	q := url.Values{"name": {name}}
	if err := c.doJSON(ctx, http.MethodPost, "/containers/create?"+q.Encode(), body, &response); err != nil {
		return "", err
	}
	if response.ID == "" {
		return "", fmt.Errorf("Docker created no container (warnings: %s)", strings.Join(response.Warnings, "; "))
	}
	return response.ID, nil
}

func (c *Client) RemoveContainer(ctx context.Context, id string, force bool) error {
	q := url.Values{"force": {fmt.Sprint(force)}, "v": {"false"}}
	return c.doJSON(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id)+"?"+q.Encode(), nil, nil)
}

func (c *Client) RemoveImage(ctx context.Context, image string) error {
	escaped := strings.ReplaceAll(url.PathEscape(image), "/", "%2F")
	return c.doJSON(ctx, http.MethodDelete, "/images/"+escaped+"?force=true", nil, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	resp, err := c.request(ctx, method, path, body, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if output == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	if err := json.NewDecoder(resp.Body).Decode(output); err != nil {
		return fmt.Errorf("decode Docker response: %w", err)
	}
	return nil
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	return c.requestWithLength(ctx, method, path, body, contentType, -1)
}

func (c *Client) requestWithLength(ctx context.Context, method, path string, body io.Reader, contentType string, length int64) (*http.Response, error) {
	if c.apiVersion != "" && path != "/version" {
		path = "/v" + c.apiVersion + path
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if length >= 0 {
		req.ContentLength = length
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var message struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &message) != nil || message.Message == "" {
			message.Message = strings.TrimSpace(string(data))
		}
		return nil, &APIError{Status: resp.StatusCode, Message: message.Message}
	}
	return resp, nil
}
