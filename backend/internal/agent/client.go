package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	socket string
	http   *http.Client
}

func NewClient(socket string) *Client {
	return &Client{
		socket: socket,
		http: &http.Client{
			Timeout: 45 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
				},
			},
		},
	}
}

func (c *Client) Shares(ctx context.Context) ([]Share, error) {
	var response struct {
		Shares []Share `json:"shares"`
	}
	if err := c.request(ctx, http.MethodGet, "/v1/shares", nil, &response); err != nil {
		return nil, err
	}
	return response.Shares, nil
}

func (c *Client) DiskHealth(ctx context.Context) ([]DiskHealth, error) {
	var response struct {
		Disks []DiskHealth `json:"disks"`
	}
	if err := c.request(ctx, http.MethodGet, "/v1/smart", nil, &response); err != nil {
		return nil, err
	}
	return response.Disks, nil
}

func (c *Client) RAIDArrays(ctx context.Context) ([]RAIDArray, error) {
	var response struct {
		Arrays []RAIDArray `json:"arrays"`
	}
	if err := c.request(ctx, http.MethodGet, "/v1/raid", nil, &response); err != nil {
		return nil, err
	}
	return response.Arrays, nil
}

func (c *Client) CreateShare(ctx context.Context, name string, permissions []Permission) (Share, error) {
	var share Share
	err := c.request(ctx, http.MethodPost, "/v1/shares", map[string]any{"name": name, "permissions": permissions}, &share)
	return share, err
}

func (c *Client) UpdateShare(ctx context.Context, id, name string, permissions []Permission) (Share, error) {
	var share Share
	err := c.request(ctx, http.MethodPut, "/v1/shares/"+url.PathEscape(id),
		map[string]any{"name": name, "permissions": permissions}, &share)
	return share, err
}

func (c *Client) DeleteShare(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, "/v1/shares/"+url.PathEscape(id), nil, nil)
}

func (c *Client) SMBUsers(ctx context.Context) ([]SMBUser, error) {
	var response struct {
		Users []SMBUser `json:"users"`
	}
	if err := c.request(ctx, http.MethodGet, "/v1/smb-users", nil, &response); err != nil {
		return nil, err
	}
	return response.Users, nil
}

func (c *Client) CreateSMBUser(ctx context.Context, username, password string) (SMBUser, error) {
	var user SMBUser
	err := c.request(ctx, http.MethodPost, "/v1/smb-users",
		map[string]string{"username": username, "password": password}, &user)
	return user, err
}

func (c *Client) UpdateSMBPassword(ctx context.Context, username, password string) error {
	return c.request(ctx, http.MethodPut, "/v1/smb-users/"+url.PathEscape(username)+"/password",
		map[string]string{"password": password}, nil)
}

func (c *Client) DeleteSMBUser(ctx context.Context, username string) error {
	return c.request(ctx, http.MethodDelete, "/v1/smb-users/"+url.PathEscape(username), nil, nil)
}

func (c *Client) ApplyRestore(ctx context.Context, id string) (RestoreJob, error) {
	var job RestoreJob
	err := c.request(ctx, http.MethodPost, "/v1/restore/apply", map[string]string{"id": id}, &job)
	return job, err
}

func (c *Client) RestoreStatus(ctx context.Context, id string) (RestoreJob, error) {
	var job RestoreJob
	err := c.request(ctx, http.MethodGet, "/v1/restore/"+url.PathEscape(id), nil, &job)
	return job, err
}

func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode ASNS Agent request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://asns-agent"+path, body)
	if err != nil {
		return fmt.Errorf("create ASNS Agent request: %w", err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("connect to ASNS Agent socket %s: %w", c.socket, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&failure); err != nil || failure.Error == "" {
			return fmt.Errorf("ASNS Agent returned HTTP %d", response.StatusCode)
		}
		return fmt.Errorf("ASNS Agent: %s", failure.Error)
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode ASNS Agent response: %w", err)
	}
	return nil
}
