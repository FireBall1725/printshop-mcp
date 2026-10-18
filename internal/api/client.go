// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package api is a minimal HTTP client for the Bambuddy and Spoolman REST
// APIs. One Client talks to one service; the two differ only in base URL and
// headers. Hand-written rather than generated because Bambuddy's spec leaves
// most response bodies untyped.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultTimeout = 30 * time.Second
	uploadTimeout  = 5 * time.Minute
)

// Client is a thin JSON-over-HTTP client for one service.
type Client struct {
	name    string
	base    string
	headers map[string]string
	hc      *http.Client
}

// New builds a client. base is the origin; paths passed to the methods start
// with /api/v1. headers are added to every request and are never logged.
func New(name, base string, headers map[string]string) *Client {
	return &Client{name: name, base: strings.TrimRight(base, "/"), headers: headers, hc: &http.Client{}}
}

// Error is a non-2xx response.
type Error struct {
	Service string
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("%s api %d", e.Service, e.Status)
	}
	return fmt.Sprintf("%s api %d: %s", e.Service, e.Status, e.Message)
}

// NotFound reports whether err is an API 404.
func NotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// Get decodes a GET response into out and returns the response headers.
func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) (http.Header, error) {
	return c.do(ctx, http.MethodGet, path, q, nil, "", defaultTimeout, out)
}

// Send sends a JSON body (nil for none) with the given method.
func (c *Client) Send(ctx context.Context, method, path string, body, out any) error {
	return c.SendQuery(ctx, method, path, nil, body, out)
}

// SendQuery is Send with query parameters, for endpoints that take their
// arguments in the URL.
func (c *Client) SendQuery(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var r io.Reader
	ct := ""
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding %s body: %w", path, err)
		}
		r, ct = bytes.NewReader(buf), "application/json"
	}
	_, err := c.do(ctx, method, path, q, r, ct, defaultTimeout, out)
	return err
}

// Upload posts r as multipart field "file".
func (c *Client) Upload(ctx context.Context, path, filename string, r io.Reader, out any) error {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", filename)
		if err == nil {
			_, err = io.Copy(part, r)
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	_, err := c.do(ctx, http.MethodPost, path, nil, pr, mw.FormDataContentType(), uploadTimeout, out)
	return err
}

// Download streams a successful response body to w, reading at most max bytes.
// A body larger than max is an error, so a runaway file cannot fill the disk.
// It returns the response headers and the bytes written.
func (c *Client) Download(ctx context.Context, path string, q url.Values, w io.Writer, max int64) (http.Header, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s unreachable: %w", c.name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.Header, 0, &Error{Service: c.name, Status: resp.StatusCode, Message: errorMessage(data)}
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, max+1))
	if err != nil {
		return resp.Header, n, fmt.Errorf("reading %s download: %w", c.name, err)
	}
	if n > max {
		return resp.Header, n, fmt.Errorf("download is larger than the %d MB limit", max>>20)
	}
	return resp.Header, n, nil
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body io.Reader, contentType string, timeout time.Duration, out any) (http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		// url.Error carries the URL, never the headers.
		return nil, fmt.Errorf("%s unreachable: %w", c.name, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("reading %s response: %w", c.name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.Header, &Error{Service: c.name, Status: resp.StatusCode, Message: errorMessage(data)}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := Decode(data, out); err != nil {
			return resp.Header, fmt.Errorf("decoding %s %s: %w", c.name, path, err)
		}
	}
	return resp.Header, nil
}

// errorMessage pulls a readable message from FastAPI ({"detail": ...}) or
// Spoolman ({"message": ...}) error bodies, falling back to trimmed text.
func errorMessage(data []byte) string {
	var env struct {
		Detail  json.RawMessage `json:"detail"`
		Message string          `json:"message"`
	}
	if Decode(data, &env) == nil {
		if env.Message != "" {
			return env.Message
		}
		if len(env.Detail) > 0 {
			var s string
			if json.Unmarshal(env.Detail, &s) == nil {
				return s
			}
			return truncate(string(env.Detail), 300)
		}
	}
	s := strings.TrimSpace(string(data))
	if strings.Contains(s, "<") {
		return ""
	}
	return truncate(s, 300)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
