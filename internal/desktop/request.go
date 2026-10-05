package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"tunnelx/internal/control"
)

// doRequest keeps the control client's status-only error and redirect policy,
// while propagating a connection attempt's cancellation through HTTP I/O.
func (e *Engine) doRequest(ctx context.Context, base, path, token string, input, output any) error {
	parent := ctx
	// Bound the entire operation, including the optional read retry.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var body io.Reader
	method := "GET"
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
		method = "POST"
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, body)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	copy := *e.http
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	started := time.Now()
	attempt := 1
	res, err := copy.Do(req)
	kind := managementErrorKind(err)
	if err != nil {
		e.recordManagement(path, method, 1, started, kind, 0)
		// Mutations may have reached the server: never replay login, purchases
		// or account edits. Certificate failures also require explicit repair.
		if method == http.MethodGet && kind != "certificate" && ctx.Err() == nil {
			copy.CloseIdleConnections()
			timer := time.NewTimer(200 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
			started = time.Now()
			attempt = 2
			res, err = copy.Do(req.Clone(ctx))
			kind = managementErrorKind(err)
			if err != nil {
				e.recordManagement(path, method, 2, started, kind, 0)
			}
		}
	}
	if err != nil {
		if parent.Err() != nil {
			return parent.Err()
		}
		return errors.New(managementErrorMessage(kind))
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		e.recordManagement(path, method, attempt, started, "http_status", res.StatusCode)
		return &control.RequestError{Status: res.StatusCode}
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(output); err != nil {
			e.recordManagement(path, method, attempt, started, "response_read", res.StatusCode)
			return errors.New("管理服务响应未完整读取或格式异常，请重新加载。")
		}
	}
	e.recordManagement(path, method, attempt, started, "ok", res.StatusCode)
	return nil
}
func (e *Engine) request(ctx context.Context, path string, input, output any) error {
	e.mu.Lock()
	base, token := e.settings.APIURL, e.login.Token
	e.mu.Unlock()
	err := e.doRequest(ctx, base, path, token, input, output)
	var response *control.RequestError
	if token == "" || !errors.As(err, &response) || response.Status != 401 {
		return err
	}
	e.mu.Lock()
	if e.login.Token != token || e.settings.APIURL != base {
		e.mu.Unlock()
		return err
	}
	e.login.Token = ""
	e.login.Email = ""
	e.stopRecoveryLocked()
	saveErr := e.saveLogin()
	cancel := e.connectCancel
	if cancel != nil {
		e.stage = "cancelling"
		cancel()
	}
	connection := e.mux
	e.mu.Unlock()
	var disconnectErr error
	// The connecting caller owns lifecycle and has no live mux; it unwinds
	// itself. Established sessions can be disconnected synchronously.
	if connection != nil {
		disconnectErr = e.disconnectSession(connection)
	}
	return errors.Join(fmt.Errorf("登录已失效，请重新登录：%w", err), saveErr, disconnectErr)
}
func (e *Engine) Query(path string, input any) (any, error) {
	allowed := map[string]bool{"/api/me": true, "/api/nodes": true, "/api/plans": true, "/api/orders": true, "/api/trial": true, "/api/release": true, "/api/public/settings": true, "/api/public/commerce": true}
	if !allowed[path] || input != nil && (path != "/api/orders" && path != "/api/trial") {
		return nil, errors.New("unsupported desktop request")
	}
	var out any
	if strings.HasPrefix(path, "/api/public/") {
		e.mu.Lock()
		base := e.settings.APIURL
		e.mu.Unlock()
		err := e.doRequest(context.Background(), base, path, "", nil, &out)
		return out, err
	}
	err := e.request(context.Background(), path, input, &out)
	return out, err
}
