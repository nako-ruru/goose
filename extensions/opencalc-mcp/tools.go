package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

type server struct {
	cfg  config
	mu   sync.Mutex
	live *liveClient
	docs map[string]*documentState
	cur  string
	mock *mockBook
}

type documentState struct {
	ID       string
	Title    string
	Sheets   []string
	Handle   string
	Status   string
	Editable bool
}

func newServer(cfg config) *server {
	s := &server{cfg: cfg, docs: map[string]*documentState{}, cur: cfg.DefaultDoc}
	if cfg.Mock {
		s.mock = newMockBook()
	}
	return s
}

func (s *server) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.live != nil {
		s.live.close()
		s.live = nil
	}
}

// ---------------------------------------------------------------------------
// 工具定义
// ---------------------------------------------------------------------------

func toolDefs() []map[string]any {
	docProp := map[string]any{
		"type":        "string",
		"description": "文档 ID（也接受标题）；省略时使用当前/默认文档",
	}
	return []map[string]any{
		withPanel(map[string]any{
			"name":        "list_documents",
			"description": "列出 OpenCalc 里的文档（ID + 标题）。先用它找到 document，再读取或写入。",
			"inputSchema": objectSchema(map[string]any{}, nil),
		}, false),
		withPanel(map[string]any{
			"name": "open_document",
			"description": "打开一个 OpenCalc 文档并加入实时协同会话（自动从 host 取 token）。" +
				"返回工作表列表和连接状态。",
			"inputSchema": objectSchema(map[string]any{"document": docProp}, nil),
		}, true),
		withPanel(map[string]any{
			"name": "read_range",
			"description": "读取 A1 区域的值，例如 A1:D20 或 Sheet2!A1:B3。返回表格预览，" +
				"并在面板里显示同一份数据。",
			"inputSchema": objectSchema(map[string]any{
				"document": docProp,
				"range":    map[string]any{"type": "string", "description": "A1 区域，如 A1:D20 或 Sheet2!A1:B3"},
			}, []string{"range"}),
		}, true),
		withPanel(map[string]any{
			"name": "write_cells",
			"description": "从锚点单元格开始，把二维数组写入文档（'=SUM(A1:A3)' 是公式，'50%' 是百分比，" +
				"前导撇号强制文本）。一次调用是一个撤销步骤。会改变已有内容时会先请求用户批准。",
			"inputSchema": objectSchema(map[string]any{
				"document": docProp,
				"anchor":   map[string]any{"type": "string", "description": "左上角单元格，如 B2 或 Sheet2!B2"},
				"values": map[string]any{
					"type":        "array",
					"description": "二维数组，每个内层数组是一行",
					"items":       map[string]any{"type": "array", "items": map[string]any{}},
				},
			}, []string{"anchor", "values"}),
		}, true),
		withPanel(map[string]any{
			"name":        "document_status",
			"description": "查看当前文档的协同连接状态、修订号、以及用户最后停留的工作表和选区。",
			"inputSchema": objectSchema(map[string]any{"document": docProp}, nil),
		}, true),
		withPanel(map[string]any{
			"name":        "recalculate",
			"description": "重新计算整份文档（公式依赖被改动后使用）。",
			"inputSchema": objectSchema(map[string]any{"document": docProp}, nil),
		}, false),
	}
}

func objectSchema(props map[string]any, required []string) map[string]any {
	s := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// withPanel 给工具加 MCP Apps 关联：Goose 读的是工具定义上的 _meta.ui.resourceUri
// （crates/goose/src/agents/extension_manager/mod.rs:573 get_tool_resource_uri）。
func withPanel(tool map[string]any, panel bool) map[string]any {
	if panel {
		tool["_meta"] = uiMeta()
	}
	return tool
}

// ---------------------------------------------------------------------------
// 文档打开
// ---------------------------------------------------------------------------

func (s *server) resolveDocID(document string) string {
	id := strings.TrimSpace(document)
	if id == "" {
		id = s.cur
	}
	if id == "" {
		id = s.cfg.DefaultDoc
	}
	if id == "" && s.cfg.Mock {
		id = "mock-demo"
	}
	return id
}

// matchKnownDoc 让 "document" 既接受 ID，也接受标题（模型经常直接复用标题）。
func (s *server) matchKnownDoc(id string) string {
	if _, ok := s.docs[id]; ok {
		return id
	}
	if s.cfg.Mock || strings.TrimSpace(id) == "" {
		return id
	}
	metas := listStore(s.cfg.Store)
	for _, m := range metas {
		if m.ID == id {
			return id
		}
	}
	lower := strings.ToLower(strings.TrimSpace(id))
	if lower == "" {
		return id
	}
	for _, m := range metas {
		if strings.ToLower(m.Title) == lower {
			return m.ID
		}
	}
	for _, m := range metas {
		if strings.Contains(strings.ToLower(m.Title), lower) {
			return m.ID
		}
	}
	return id
}

// ensureDocLocked 调用方持有 s.mu。
func (s *server) ensureDocLocked(document string) (*documentState, error) {
	id := s.resolveDocID(document)
	if id == "" {
		return nil, errNoDocument
	}
	id = s.matchKnownDoc(id)
	if st := s.docs[id]; st != nil {
		if s.cfg.Mock {
			return st, nil
		}
		if st.Handle != "" && s.live != nil && s.live.alive() {
			return st, nil
		}
		delete(s.docs, id) // sidecar 重启过，handle 失效
	}
	if s.cfg.Mock {
		return s.openMockLocked(id)
	}

	sess, err := mintSession(s.cfg.Host, id, s.cfg.Actor)
	if err != nil {
		return nil, err
	}
	collab := s.cfg.CollabURL
	if collab == "" {
		collab = sess.Collab
	}
	if s.live != nil && !s.live.alive() {
		s.live.close()
		s.live = nil
	}
	if s.live == nil {
		l, err := startLive(s.cfg.LiveBin, collab)
		if err != nil {
			return nil, err
		}
		s.live = l
	}
	args := map[string]any{"doc": id, "token": sess.Token, "collab_url": sess.Collab}
	if s.cfg.CollabURL != "" {
		args["collab_url"] = s.cfg.CollabURL
	}
	raw, err := s.live.tool("open_workbook", args, 90*time.Second)
	if err != nil {
		return nil, err
	}
	var opened struct {
		Handle    string   `json:"handle"`
		Sheets    []string `json:"sheets"`
		Status    string   `json:"status"`
		Connected bool     `json:"connected"`
		Editable  *bool    `json:"editable"`
	}
	if err := json.Unmarshal([]byte(raw), &opened); err != nil {
		return nil, fmt.Errorf("解析 open_workbook 结果失败: %w", err)
	}
	if opened.Handle == "" {
		return nil, errors.New("open_workbook 没有返回 handle")
	}
	title := sess.Title
	if title == "" {
		title = id
	}
	st := &documentState{
		ID:       id,
		Title:    title,
		Sheets:   opened.Sheets,
		Handle:   opened.Handle,
		Status:   opened.Status,
		Editable: opened.Editable == nil || *opened.Editable,
	}
	if st.Status == "" {
		if opened.Connected {
			st.Status = "connected"
		} else {
			st.Status = "opening"
		}
	}
	s.docs[id] = st
	s.cur = id
	return st, nil
}

// ---------------------------------------------------------------------------
// 工具实现
// ---------------------------------------------------------------------------

func (s *server) listDocuments() (string, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	metas := listStore(s.cfg.Store)
	if len(metas) == 0 && s.cfg.Mock {
		metas = []docMeta{{ID: "mock-demo", Title: "OpenCalc Mock Demo.xlsx"}}
	}
	seen := map[string]bool{}
	lines := []string{}
	docs := []map[string]any{}
	for _, m := range metas {
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		lines = append(lines, fmt.Sprintf("- %s — %s", m.ID, m.Title))
		docs = append(docs, map[string]any{"id": m.ID, "title": m.Title})
	}
	if s.cfg.DefaultDoc != "" && !seen[s.cfg.DefaultDoc] {
		docs = append(docs, map[string]any{"id": s.cfg.DefaultDoc, "title": s.cfg.DefaultDoc})
		lines = append(lines, fmt.Sprintf("- %s — %s（默认）", s.cfg.DefaultDoc, s.cfg.DefaultDoc))
	}
	if len(docs) == 0 {
		return "没有找到文档：设置 OPENCALC_DOC，或用 open_document 指定文档 ID。" +
			"（若 host 数据目录不同，设置 OPENCALC_STORE）", map[string]any{"documents": docs}, nil
	}
	return fmt.Sprintf("共 %d 个文档（document 参数优先用前面的 ID，也接受标题）：\n%s",
			len(docs), strings.Join(lines, "\n")),
		map[string]any{"documents": docs}, nil
}

func (s *server) openDocument(args json.RawMessage) (string, any, error) {
	var a struct {
		Document string `json:"document"`
	}
	_ = json.Unmarshal(args, &a)

	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.ensureDocLocked(a.Document)
	if err != nil {
		return "", nil, err
	}
	text := fmt.Sprintf("已打开《%s》（%s），状态 %s，%d 张工作表：\n- %s",
		st.Title, st.ID, st.Status, len(st.Sheets), strings.Join(st.Sheets, "\n- "))
	return text, map[string]any{
		"document": st.ID,
		"title":    st.Title,
		"sheets":   st.Sheets,
		"status":   st.Status,
		"editable": st.Editable,
	}, nil
}

func (s *server) readRange(args json.RawMessage) (string, any, error) {
	var a struct {
		Document string `json:"document"`
		Range    string `json:"range"`
	}
	_ = json.Unmarshal(args, &a)
	rangeStr := strings.TrimSpace(a.Range)
	if rangeStr == "" {
		rangeStr = "A1:D20"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.ensureDocLocked(a.Document)
	if err != nil {
		return "", nil, err
	}

	var sheetIdx int
	var values [][]any
	var rangeLabel string
	if s.cfg.Mock {
		sheetIdx, values, err = s.mock.readRange(rangeStr)
		rangeLabel = rangeStr
	} else {
		raw, err2 := s.live.tool("read_range", map[string]any{
			"handle": st.Handle, "range": rangeStr,
		}, 30*time.Second)
		if err2 != nil {
			return "", nil, err2
		}
		var out struct {
			Sheet  int     `json:"sheet"`
			Range  string  `json:"range"`
			Values [][]any `json:"values"`
		}
		if err2 := json.Unmarshal([]byte(raw), &out); err2 != nil {
			return "", nil, fmt.Errorf("解析 read_range 结果失败: %w", err2)
		}
		sheetIdx, values, rangeLabel = out.Sheet, out.Values, out.Range
	}
	if err != nil {
		return "", nil, err
	}

	sheetName := ""
	if sheetIdx >= 0 && sheetIdx < len(st.Sheets) {
		sheetName = st.Sheets[sheetIdx]
	}
	r1, c1, _, c2, perr := parseRange(rangeStr)
	if perr != nil {
		r1, c1, c2 = 1, 1, 1
		for _, row := range values {
			if len(row) > c2 {
				c2 = len(row)
			}
		}
	}
	text := renderGrid(st.Title, sheetName, rangeLabel, r1, c1, values)
	structured := map[string]any{
		"document":   st.ID,
		"title":      st.Title,
		"sheet":      sheetName,
		"sheetIndex": sheetIdx,
		"range":      rangeLabel,
		"startRow":   r1,
		"startCol":   c1,
		"values":     values,
	}
	return text, structured, nil
}

func (s *server) writeCells(args json.RawMessage) (string, any, error) {
	var a struct {
		Document string          `json:"document"`
		Anchor   string          `json:"anchor"`
		Values   json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", nil, fmt.Errorf("参数错误: %w", err)
	}
	if strings.TrimSpace(a.Anchor) == "" {
		return "", nil, errors.New("anchor 不能为空")
	}
	if len(a.Values) == 0 {
		return "", nil, errors.New("values 不能为空")
	}
	var rows [][]any
	if err := json.Unmarshal(a.Values, &rows); err != nil {
		// 允许一维数组：当作单行
		var one []any
		if err2 := json.Unmarshal(a.Values, &one); err2 != nil {
			return "", nil, fmt.Errorf("values 必须是二维数组: %w", err)
		}
		rows = [][]any{one}
	}
	written := 0
	for _, row := range rows {
		written += len(row)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.ensureDocLocked(a.Document)
	if err != nil {
		return "", nil, err
	}

	if s.cfg.Mock {
		err = s.mock.writeCells(a.Anchor, rows)
	} else {
		var rawArgs any = rows
		_, err = s.live.tool("write_cells", map[string]any{
			"handle": st.Handle, "anchor": a.Anchor, "values": rawArgs,
		}, 300*time.Second) // 覆盖大量单元格时会等人批准，sidecar 最多等 240s
	}
	if err != nil {
		return "", nil, err
	}

	r1, c1, _, _, perr := parseRange(a.Anchor)
	if perr != nil {
		r1, c1 = 0, 0
	}
	cols := 0
	for _, row := range rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	// 把写入的块也在面板里显示出来（只含写入值，不是整张表）。
	sheet := sheetForAnchor(st, a.Anchor)
	ref := a.Anchor
	if !strings.Contains(ref, "!") && sheet != "" {
		ref = sheet + "!" + ref
	}
	// 把写入的块也在面板里显示出来（只含写入值，不是整张表）。
	text := fmt.Sprintf("已写入 %d 个单元格到《%s》%s（%d 行 × %d 列）",
		written, st.Title, ref, len(rows), cols)
	if perr == nil && cols > 0 {
		text += "\n\n" + renderGrid(st.Title, sheet, a.Anchor, r1, c1, rows)
	}
	return text, map[string]any{
		"document": st.ID,
		"title":    st.Title,
		"sheet":    sheet,
		"range":    ref,
		"startRow": r1,
		"startCol": c1,
		"values":   rows,
		"written":  written,
	}, nil
}

func (s *server) documentStatus(args json.RawMessage) (string, any, error) {
	var a struct {
		Document string `json:"document"`
	}
	_ = json.Unmarshal(args, &a)

	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.resolveDocID(a.Document)
	if id == "" {
		return "还没有打开任何文档。", map[string]any{"documents": []any{}}, nil
	}
	st := s.docs[id]
	if st == nil {
		// 状态查询不应该顺带建立连接；只报告未打开。
		return fmt.Sprintf("《%s》还没有打开；调用 open_document 或 read_range 时会自动打开。", id),
			map[string]any{"document": id, "status": "not-open"}, nil
	}
	if s.cfg.Mock {
		return fmt.Sprintf("《%s》：mock 模式，状态 connected，%d 张工作表。", st.Title, len(st.Sheets)),
			map[string]any{"document": st.ID, "title": st.Title, "status": "connected", "editable": true}, nil
	}
	raw, err := s.live.tool("status", map[string]any{"handle": st.Handle}, 20*time.Second)
	if err != nil {
		return "", nil, err
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return "", nil, fmt.Errorf("解析 status 结果失败: %w", err)
	}
	status, _ := out["status"].(string)
	connected, _ := out["connected"].(bool)
	revision := out["revision"]
	text := fmt.Sprintf("《%s》（%s）：status=%s connected=%v revision=%v editable=%v",
		st.Title, st.ID, status, connected, revision, out["editable"])
	if viewer, ok := out["viewer"].(map[string]any); ok {
		text += fmt.Sprintf("\n用户当前在：%v / %v / %v",
			viewer["name"], viewer["sheetName"], viewer["selection"])
	}
	out["document"] = st.ID
	out["title"] = st.Title
	return text, out, nil
}

func (s *server) recalculate(args json.RawMessage) (string, any, error) {
	var a struct {
		Document string `json:"document"`
	}
	_ = json.Unmarshal(args, &a)

	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.ensureDocLocked(a.Document)
	if err != nil {
		return "", nil, err
	}
	if !s.cfg.Mock {
		if _, err := s.live.tool("recalculate", map[string]any{"handle": st.Handle}, 60*time.Second); err != nil {
			return "", nil, err
		}
	}
	return fmt.Sprintf("已重新计算《%s》。", st.Title),
		map[string]any{"document": st.ID, "title": st.Title, "recalculated": true}, nil
}

// ---------------------------------------------------------------------------
// 渲染
// ---------------------------------------------------------------------------

func renderGrid(title, sheet, rangeLabel string, startRow, startCol int, values [][]any) string {
	const maxRows = 40
	const maxCols = 12
	cols := 0
	for _, row := range values {
		if len(row) > cols {
			cols = len(row)
		}
	}
	header := fmt.Sprintf("《%s》", title)
	if sheet != "" {
		header += " · " + sheet
	}
	header += fmt.Sprintf(" · %s（%d 行 × %d 列）", rangeLabel, len(values), cols)
	if len(values) == 0 || cols == 0 {
		return header + "\n（区域为空）"
	}

	shownCols := cols
	if shownCols > maxCols {
		shownCols = maxCols
	}
	shownRows := len(values)
	if shownRows > maxRows {
		shownRows = maxRows
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n|  |")
	for c := 0; c < shownCols; c++ {
		fmt.Fprintf(&b, " %s |", colName(startCol+c))
	}
	b.WriteString("\n|---|")
	for c := 0; c < shownCols; c++ {
		b.WriteString("---|")
	}
	for r := 0; r < shownRows; r++ {
		fmt.Fprintf(&b, "\n| %d |", startRow+r)
		row := values[r]
		for c := 0; c < shownCols; c++ {
			v := ""
			if c < len(row) {
				v = cellString(row[c])
			}
			fmt.Fprintf(&b, " %s |", strings.ReplaceAll(v, "|", `\|`))
		}
	}
	if shownRows < len(values) || shownCols < cols {
		fmt.Fprintf(&b, "\n\n（已截断：只显示前 %d 行 × %d 列；需要更多请缩小 range）", shownRows, shownCols)
	}
	return b.String()
}

func cellString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.ReplaceAll(strings.ReplaceAll(x, "\r", " "), "\n", " ")
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func sheetForAnchor(st *documentState, ref string) string {
	if sheet, _, err := splitRef(ref); err == nil && sheet != "" {
		return sheet
	}
	if len(st.Sheets) > 0 {
		return st.Sheets[0]
	}
	return ""
}

// ---------------------------------------------------------------------------
// A1 地址
// ---------------------------------------------------------------------------

func colName(i int) string {
	if i <= 0 {
		return ""
	}
	name := ""
	for i > 0 {
		i--
		name = string(rune('A'+i%26)) + name
		i /= 26
	}
	return name
}

func colIndex(letters string) (int, error) {
	if letters == "" {
		return 0, errors.New("空列名")
	}
	n := 0
	for _, r := range strings.ToUpper(letters) {
		if r < 'A' || r > 'Z' {
			return 0, fmt.Errorf("非法列名 %q", letters)
		}
		n = n*26 + int(r-'A'+1)
	}
	return n, nil
}

// parseCell 解析 A1 / $B$7，返回 1-based 行列。
func parseCell(s string) (row, col int, err error) {
	s = strings.ReplaceAll(s, "$", "")
	i := 0
	for i < len(s) && (s[i] >= 'A' && s[i] <= 'Z' || s[i] >= 'a' && s[i] <= 'z') {
		i++
	}
	if i == 0 || i == len(s) {
		return 0, 0, fmt.Errorf("非法单元格 %q", s)
	}
	col, err = colIndex(s[:i])
	if err != nil {
		return 0, 0, err
	}
	row, err = strconv.Atoi(s[i:])
	if err != nil || row <= 0 {
		return 0, 0, fmt.Errorf("非法单元格 %q", s)
	}
	return row, col, nil
}

func splitRef(ref string) (sheet string, rest string, err error) {
	ref = strings.TrimSpace(ref)
	if i := strings.LastIndex(ref, "!"); i >= 0 {
		sheet = strings.Trim(ref[:i], "'")
		rest = ref[i+1:]
		return sheet, rest, nil
	}
	return "", ref, nil
}

// parseRange 解析 A1:D5（也接受单格 A1 与 Sheet2!A1:B3），返回 1-based 起止行列。
func parseRange(ref string) (r1, c1, r2, c2 int, err error) {
	_, rest, err := splitRef(ref)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	parts := strings.SplitN(rest, ":", 2)
	r1, c1, err = parseCell(parts[0])
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if len(parts) == 1 {
		return r1, c1, r1, c1, nil
	}
	r2, c2, err = parseCell(parts[1])
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return r1, c1, r2, c2, nil
}
