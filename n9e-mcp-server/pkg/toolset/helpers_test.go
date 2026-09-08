package toolset

import (
	"testing"
)

func TestSlicePage(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	tests := []struct {
		name      string
		items     []int
		page      int
		limit     int
		wantItems []int
		wantTotal int64
	}{
		{
			name:      "first page",
			items:     items,
			page:      1,
			limit:     3,
			wantItems: []int{1, 2, 3},
			wantTotal: 10,
		},
		{
			name:      "middle page",
			items:     items,
			page:      2,
			limit:     3,
			wantItems: []int{4, 5, 6},
			wantTotal: 10,
		},
		{
			name:      "last page partial",
			items:     items,
			page:      4,
			limit:     3,
			wantItems: []int{10},
			wantTotal: 10,
		},
		{
			name:      "page beyond range",
			items:     items,
			page:      100,
			limit:     3,
			wantItems: []int{},
			wantTotal: 10,
		},
		{
			name:      "default limit and page",
			items:     items,
			page:      0,
			limit:     0,
			wantItems: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			wantTotal: 10,
		},
		{
			name:      "negative page and limit use defaults",
			items:     items,
			page:      -1,
			limit:     -5,
			wantItems: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			wantTotal: 10,
		},
		{
			name:      "empty slice",
			items:     []int{},
			page:      1,
			limit:     5,
			wantItems: []int{},
			wantTotal: 0,
		},
		{
			name:      "nil slice",
			items:     nil,
			page:      1,
			limit:     5,
			wantItems: []int{},
			wantTotal: 0,
		},
		{
			name:      "limit larger than total",
			items:     items,
			page:      1,
			limit:     100,
			wantItems: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			wantTotal: 10,
		},
		{
			name:      "single item",
			items:     []int{42},
			page:      1,
			limit:     5,
			wantItems: []int{42},
			wantTotal: 1,
		},
		{
			name:      "single item page 2",
			items:     []int{42},
			page:      2,
			limit:     5,
			wantItems: []int{},
			wantTotal: 1,
		},
		{
			name:      "exact page boundary",
			items:     []int{1, 2, 3, 4, 5, 6},
			page:      2,
			limit:     3,
			wantItems: []int{4, 5, 6},
			wantTotal: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotItems, gotTotal := SlicePage(tt.items, tt.page, tt.limit)

			if gotTotal != tt.wantTotal {
				t.Errorf("total = %d, want %d", gotTotal, tt.wantTotal)
			}

			if len(gotItems) != len(tt.wantItems) {
				t.Errorf("len(items) = %d, want %d", len(gotItems), len(tt.wantItems))
				return
			}

			for i, v := range gotItems {
				if v != tt.wantItems[i] {
					t.Errorf("items[%d] = %d, want %d", i, v, tt.wantItems[i])
				}
			}
		})
	}
}

func TestEnableToolsetsSplitsCommaEnv(t *testing.T) {
	g := NewToolsetGroup(true)
	g.AddToolset(NewToolset("alerts", "a"))
	g.AddToolset(NewToolset("metrics", "m"))
	g.AddToolset(NewToolset("logs", "l"))
	g.AddToolset(NewToolset("users", "u"))

	if err := g.EnableToolsets([]string{"alerts,metrics,logs"}); err != nil {
		t.Fatalf("EnableToolsets: %v", err)
	}
	enabled := map[string]bool{}
	for _, name := range g.GetEnabledToolsets() {
		enabled[name] = true
	}
	for _, want := range []string{"alerts", "metrics", "logs"} {
		if !enabled[want] {
			t.Errorf("expected %s enabled", want)
		}
	}
	if enabled["users"] {
		t.Error("users should stay disabled")
	}
}
