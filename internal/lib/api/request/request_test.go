package request

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    *Request
		wantErr error
	}{
		{
			name: "valid request with all fields",
			body: `{"data":[{"name":"test"}],"method":"create","full_update":true,"page":2,"count":20}`,
			want: &Request{
				Data:       []interface{}{map[string]interface{}{"name": "test"}},
				Method:     "create",
				FullUpdate: true,
				Page:       2,
				Count:      20,
			},
			wantErr: nil,
		},
		{
			name: "valid request with minimal fields",
			body: `{"method":"list"}`,
			want: &Request{
				Method: "list",
			},
			wantErr: nil,
		},
		{
			name:    "empty body",
			body:    "",
			want:    nil,
			wantErr: ErrEmptyBody,
		},
		{
			name:    "invalid json",
			body:    `{"invalid json`,
			want:    nil,
			wantErr: io.EOF, // Will be a different error but not ErrEmptyBody
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/test", bytes.NewBufferString(tt.body))
			got, err := Decode(req)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("Decode() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.wantErr == ErrEmptyBody && err != ErrEmptyBody {
					t.Errorf("Decode() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("Decode() unexpected error = %v", err)
				return
			}

			if got.Method != tt.want.Method {
				t.Errorf("Decode() Method = %v, want %v", got.Method, tt.want.Method)
			}
			if got.FullUpdate != tt.want.FullUpdate {
				t.Errorf("Decode() FullUpdate = %v, want %v", got.FullUpdate, tt.want.FullUpdate)
			}
			if got.Page != tt.want.Page {
				t.Errorf("Decode() Page = %v, want %v", got.Page, tt.want.Page)
			}
			if got.Count != tt.want.Count {
				t.Errorf("Decode() Count = %v, want %v", got.Count, tt.want.Count)
			}
		})
	}
}

func TestRequest_GetPagination(t *testing.T) {
	tests := []struct {
		name       string
		page       int
		count      int
		wantOffset int
		wantLimit  int
	}{
		{
			name:       "first page",
			page:       1,
			count:      10,
			wantOffset: 0,
			wantLimit:  10,
		},
		{
			name:       "second page",
			page:       2,
			count:      10,
			wantOffset: 10,
			wantLimit:  10,
		},
		{
			name:       "third page with 20 items",
			page:       3,
			count:      20,
			wantOffset: 40,
			wantLimit:  20,
		},
		{
			name:       "default count when zero",
			page:       1,
			count:      0,
			wantOffset: 0,
			wantLimit:  100,
		},
		{
			name:       "default page when zero",
			page:       0,
			count:      20,
			wantOffset: 0,
			wantLimit:  20,
		},
		{
			name:       "default page when negative",
			page:       -1,
			count:      20,
			wantOffset: 0,
			wantLimit:  20,
		},
		{
			name:       "both defaults",
			page:       0,
			count:      0,
			wantOffset: 0,
			wantLimit:  100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Request{
				Page:  tt.page,
				Count: tt.count,
			}
			gotOffset, gotLimit := r.GetPagination()
			if gotOffset != tt.wantOffset {
				t.Errorf("GetPagination() offset = %v, want %v", gotOffset, tt.wantOffset)
			}
			if gotLimit != tt.wantLimit {
				t.Errorf("GetPagination() limit = %v, want %v", gotLimit, tt.wantLimit)
			}
		})
	}
}

func TestRequest_UnmarshalData(t *testing.T) {
	tests := []struct {
		name    string
		data    interface{}
		target  interface{}
		wantErr bool
	}{
		{
			name: "unmarshal array of maps",
			data: []interface{}{
				map[string]interface{}{"id": "1", "name": "test"},
			},
			target:  &[]map[string]interface{}{},
			wantErr: false,
		},
		{
			name:    "nil data",
			data:    nil,
			target:  &[]map[string]interface{}{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Request{
				Data: tt.data,
			}
			err := r.UnmarshalData(tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalData() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
