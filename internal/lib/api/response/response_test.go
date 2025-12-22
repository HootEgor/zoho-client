package response

import (
	"testing"
)

func TestOk(t *testing.T) {
	data := map[string]string{"key": "value"}
	resp := Ok(data)

	if !resp.Success {
		t.Error("Ok() Success should be true")
	}

	if resp.StatusMessage != "Success" {
		t.Errorf("Ok() StatusMessage = %v, want Success", resp.StatusMessage)
	}

	if resp.Data == nil {
		t.Error("Ok() Data should not be nil")
	}

	if resp.Timestamp == "" {
		t.Error("Ok() Timestamp should not be empty")
	}

	if resp.Pagination != nil {
		t.Error("Ok() Pagination should be nil")
	}
}

func TestOkWithPagination(t *testing.T) {
	tests := []struct {
		name           string
		data           interface{}
		page           int
		count          int
		total          int
		wantTotalPages int
	}{
		{
			name:           "exact pages",
			data:           []string{"item1", "item2"},
			page:           1,
			count:          10,
			total:          100,
			wantTotalPages: 10,
		},
		{
			name:           "partial last page",
			data:           []string{"item1", "item2"},
			page:           1,
			count:          10,
			total:          95,
			wantTotalPages: 10,
		},
		{
			name:           "single page",
			data:           []string{"item1"},
			page:           1,
			count:          20,
			total:          5,
			wantTotalPages: 1,
		},
		{
			name:           "empty results",
			data:           []string{},
			page:           1,
			count:          10,
			total:          0,
			wantTotalPages: 0,
		},
		{
			name:           "zero count",
			data:           []string{},
			page:           1,
			count:          0,
			total:          100,
			wantTotalPages: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := OkWithPagination(tt.data, tt.page, tt.count, tt.total)

			if !resp.Success {
				t.Error("OkWithPagination() Success should be true")
			}

			if resp.StatusMessage != "Success" {
				t.Errorf("OkWithPagination() StatusMessage = %v, want Success", resp.StatusMessage)
			}

			if resp.Pagination == nil {
				t.Fatal("OkWithPagination() Pagination should not be nil")
			}

			if resp.Pagination.Page != tt.page {
				t.Errorf("OkWithPagination() Page = %v, want %v", resp.Pagination.Page, tt.page)
			}

			if resp.Pagination.Count != tt.count {
				t.Errorf("OkWithPagination() Count = %v, want %v", resp.Pagination.Count, tt.count)
			}

			if resp.Pagination.Total != tt.total {
				t.Errorf("OkWithPagination() Total = %v, want %v", resp.Pagination.Total, tt.total)
			}

			if resp.Pagination.TotalPages != tt.wantTotalPages {
				t.Errorf("OkWithPagination() TotalPages = %v, want %v", resp.Pagination.TotalPages, tt.wantTotalPages)
			}

			if resp.Timestamp == "" {
				t.Error("OkWithPagination() Timestamp should not be empty")
			}
		})
	}
}

func TestError(t *testing.T) {
	message := "Test error message"
	resp := Error(message)

	if resp.Success {
		t.Error("Error() Success should be false")
	}

	if resp.StatusMessage != message {
		t.Errorf("Error() StatusMessage = %v, want %v", resp.StatusMessage, message)
	}

	if resp.Data != nil {
		t.Error("Error() Data should be nil")
	}

	if resp.Timestamp == "" {
		t.Error("Error() Timestamp should not be empty")
	}

	if resp.Pagination != nil {
		t.Error("Error() Pagination should be nil")
	}
}

func TestPaginationCalculations(t *testing.T) {
	tests := []struct {
		name           string
		count          int
		total          int
		wantTotalPages int
	}{
		{"100 items, 10 per page", 10, 100, 10},
		{"95 items, 10 per page", 10, 95, 10},
		{"91 items, 10 per page", 10, 91, 10},
		{"90 items, 10 per page", 10, 90, 9},
		{"1 item, 10 per page", 10, 1, 1},
		{"0 items, 10 per page", 10, 0, 0},
		{"100 items, 1 per page", 1, 100, 100},
		{"100 items, 100 per page", 100, 100, 1},
		{"101 items, 100 per page", 100, 101, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := OkWithPagination(nil, 1, tt.count, tt.total)
			if resp.Pagination.TotalPages != tt.wantTotalPages {
				t.Errorf("TotalPages = %v, want %v (count=%d, total=%d)",
					resp.Pagination.TotalPages, tt.wantTotalPages, tt.count, tt.total)
			}
		})
	}
}
