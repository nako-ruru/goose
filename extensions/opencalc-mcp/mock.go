package main

import (
	"fmt"
	"strings"
)

// mockBook 只在 OPENCALC_MOCK=1 时使用：它验证的是 MCP 契约（工具、_meta、面板
// 的 structuredContent），不是 OpenCalc 的协同/公式引擎。默认不启用。
type mockBook struct {
	sheets []string
	cells  []map[string]any
}

func newMockBook() *mockBook {
	b := &mockBook{
		sheets: []string{"Sheet1", "Notes"},
		cells:  []map[string]any{{}, {}},
	}
	seed := [][]any{
		{"Item", "Qty", "Price", "Total"},
		{"Widget", 12, 3.5, "=B2*C2"},
		{"Gadget", 4, 12.25, "=B3*C3"},
		{"Service", 1, 200, "=B4*C4"},
		{"", "", "", "=SUM(D2:D4)"},
	}
	b.writeCells("A1", seed)
	b.cells[0]["A8"] = "（mock 数据，未连接 opencalc-live）"
	return b
}

func (b *mockBook) sheetIndex(name string) (int, error) {
	if name == "" {
		return 0, nil
	}
	for i, s := range b.sheets {
		if strings.EqualFold(s, name) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("mock: 没有工作表 %q", name)
}

func (b *mockBook) readRange(ref string) (int, [][]any, error) {
	sheet, rest, _ := splitRef(ref)
	idx, err := b.sheetIndex(sheet)
	if err != nil {
		return 0, nil, err
	}
	r1, c1, r2, c2, err := parseRange(rest)
	if err != nil {
		return 0, nil, err
	}
	if r2 < r1 || c2 < c1 {
		return 0, nil, fmt.Errorf("mock: 非法区域 %q", ref)
	}
	values := make([][]any, 0, r2-r1+1)
	for r := r1; r <= r2; r++ {
		row := make([]any, 0, c2-c1+1)
		for c := c1; c <= c2; c++ {
			row = append(row, b.cells[idx][fmt.Sprintf("%s%d", colName(c), r)])
		}
		values = append(values, row)
	}
	return idx, values, nil
}

func (b *mockBook) writeCells(anchor string, rows [][]any) error {
	sheet, rest, _ := splitRef(anchor)
	idx, err := b.sheetIndex(sheet)
	if err != nil {
		return err
	}
	r1, c1, _, _, err := parseRange(rest)
	if err != nil {
		return err
	}
	for r, row := range rows {
		for c, v := range row {
			key := fmt.Sprintf("%s%d", colName(c1+c), r1+r)
			if v == nil {
				delete(b.cells[idx], key)
				continue
			}
			b.cells[idx][key] = v
		}
	}
	return nil
}

func (s *server) openMockLocked(id string) (*documentState, error) {
	title := "OpenCalc Mock Demo.xlsx"
	if id != "" && id != "mock-demo" {
		title = id + "（mock）"
	}
	st := &documentState{
		ID:       id,
		Title:    title,
		Sheets:   s.mock.sheets,
		Handle:   "mock",
		Status:   "connected",
		Editable: true,
	}
	s.docs[id] = st
	s.cur = id
	return st, nil
}
