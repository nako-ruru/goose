package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type config struct {
	Host       string
	Store      string
	DefaultDoc string
	LiveBin    string
	CollabURL  string
	Actor      string
	Mock       bool
}

func loadConfig() config {
	c := config{
		Host:       strings.TrimRight(envOr("OPENCALC_HOST", "http://127.0.0.1:8088"), "/"),
		Store:      strings.TrimSpace(os.Getenv("OPENCALC_STORE")),
		DefaultDoc: strings.TrimSpace(os.Getenv("OPENCALC_DOC")),
		LiveBin:    strings.TrimSpace(os.Getenv("OPENCALC_LIVE_BIN")),
		CollabURL:  strings.TrimSpace(os.Getenv("OPENCALC_COLLAB_URL")),
		Actor:      envOr("OPENCALC_ACTOR", "AI"),
		Mock:       truthy(os.Getenv("OPENCALC_MOCK")),
	}
	if c.Store == "" {
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			p := filepath.Join(la, "Rimuse", "opencalc", "data")
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				c.Store = p
			}
		}
	}
	if c.LiveBin == "" {
		if p, err := exec.LookPath("opencalc-live"); err == nil {
			c.LiveBin = p
		} else if la := os.Getenv("LOCALAPPDATA"); la != "" {
			p := filepath.Join(la, "Rimuse", "opencalc", "bin", "opencalc-live.exe")
			if _, err := os.Stat(p); err == nil {
				c.LiveBin = p
			}
		}
	}
	return c
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// host: /api/documents/{id}/session 铸造 token
// ---------------------------------------------------------------------------

type sessionInfo struct {
	Collab   string `json:"collab"`
	Document string `json:"document"`
	Title    string `json:"title"`
	Token    string `json:"token"`
	Editable bool   `json:"editable"`
}

func mintSession(host, doc, actor string) (*sessionInfo, error) {
	u := fmt.Sprintf("%s/api/documents/%s/session?name=%s",
		host, url.PathEscape(doc), url.QueryEscape(actor))
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 OpenCalc host 失败（%s）：%w", host, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("host 拒绝拿 token（HTTP %d，文档 %s）：%s", resp.StatusCode, doc, msg)
	}
	var out sessionInfo
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析 session 响应失败: %w", err)
	}
	if out.Token == "" || out.Collab == "" {
		return nil, errors.New("session 响应缺少 token/collab")
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// opencalc-live 子进程：一行一个 JSON-RPC 消息
// ---------------------------------------------------------------------------

type liveClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	mu      sync.Mutex
	pending map[int]chan json.RawMessage
	nextID  int
	closed  bool
}

func startLive(bin, collab string) (*liveClient, error) {
	if bin == "" {
		return nil, errors.New("找不到 opencalc-live：设置 OPENCALC_LIVE_BIN")
	}
	args := []string{}
	if collab != "" {
		args = append(args, "--collab-url", collab)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stderr = os.Stderr // sidecar 的日志走 stderr，不会污染 MCP 的 stdout
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 opencalc-live 失败: %w", err)
	}
	l := &liveClient{cmd: cmd, stdin: in, pending: map[int]chan json.RawMessage{}}
	go l.readLoop(outPipe)
	return l, nil
}

func (l *liveClient) readLoop(out io.Reader) {
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(line, &msg); err != nil || len(msg.ID) == 0 {
			continue
		}
		var id int
		if err := json.Unmarshal(msg.ID, &id); err != nil {
			continue
		}
		l.mu.Lock()
		ch := l.pending[id]
		delete(l.pending, id)
		l.mu.Unlock()
		if ch != nil {
			ch <- append(json.RawMessage(nil), line...)
		}
	}
	l.mu.Lock()
	l.closed = true
	for id, ch := range l.pending {
		close(ch)
		delete(l.pending, id)
	}
	l.mu.Unlock()
}

func (l *liveClient) alive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.closed
}

func (l *liveClient) call(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, errors.New("opencalc-live 已退出")
	}
	id := l.nextID
	l.nextID++
	ch := make(chan json.RawMessage, 1)
	l.pending[id] = ch
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err == nil {
		_, err = l.stdin.Write(append(payload, '\n'))
	}
	l.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("写入 sidecar 失败: %w", err)
	}

	select {
	case raw, open := <-ch:
		if !open {
			return nil, errors.New("opencalc-live 已退出")
		}
		var env struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("sidecar 返回无法解析: %w", err)
		}
		if env.Error != nil {
			return nil, fmt.Errorf("opencalc-live: %s", env.Error.Message)
		}
		return env.Result, nil
	case <-time.After(timeout):
		l.mu.Lock()
		delete(l.pending, id)
		l.mu.Unlock()
		return nil, fmt.Errorf("opencalc-live 超时（%s）", timeout)
	}
}

// tool 调 sidecar 的 tools/call，取第一条 text content。
func (l *liveClient) tool(name string, args map[string]any, timeout time.Duration) (string, error) {
	raw, err := l.call("tools/call", map[string]any{"name": name, "arguments": args}, timeout)
	if err != nil {
		return "", err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("解析 sidecar 工具结果失败: %w", err)
	}
	var text string
	for _, c := range out.Content {
		if c.Type == "text" {
			text = c.Text
			break
		}
	}
	if out.IsError {
		if text == "" {
			text = "opencalc-live 返回错误"
		}
		return "", errors.New(text)
	}
	return text, nil
}

func (l *liveClient) close() {
	l.mu.Lock()
	closed := l.closed
	l.closed = true
	l.mu.Unlock()
	if closed {
		return
	}
	_ = l.stdin.Close()
	done := make(chan struct{})
	go func() {
		_ = l.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = l.cmd.Process.Kill()
	}
}

// ---------------------------------------------------------------------------
// store 里的文档清单（host 没有 list 接口，直接读它的数据目录）
// ---------------------------------------------------------------------------

type docMeta struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func listStore(dir string) []docMeta {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []docMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var m docMeta
		if json.Unmarshal(raw, &m) != nil || m.ID == "" {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out
}
