package request

import (
	"testing"
)

type TestEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestDecodeArrayData(t *testing.T) {
	tests := []struct {
		name    string
		data    interface{}
		want    []TestEntity
		wantErr bool
	}{
		{
			name: "valid array with multiple items",
			data: []interface{}{
				map[string]interface{}{"id": "1", "name": "First"},
				map[string]interface{}{"id": "2", "name": "Second"},
			},
			want: []TestEntity{
				{ID: "1", Name: "First"},
				{ID: "2", Name: "Second"},
			},
			wantErr: false,
		},
		{
			name: "valid array with single item",
			data: []interface{}{
				map[string]interface{}{"id": "1", "name": "Single"},
			},
			want: []TestEntity{
				{ID: "1", Name: "Single"},
			},
			wantErr: false,
		},
		{
			name:    "nil data returns empty array",
			data:    nil,
			want:    []TestEntity{},
			wantErr: false,
		},
		{
			name:    "empty array",
			data:    []interface{}{},
			want:    []TestEntity{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &Request{Data: tt.data}
			var got []TestEntity
			err := DecodeArrayData(req, &got)

			if (err != nil) != tt.wantErr {
				t.Errorf("DecodeArrayData() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil {
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("DecodeArrayData() length = %v, want %v", len(got), len(tt.want))
				return
			}

			for i := range got {
				if got[i].ID != tt.want[i].ID || got[i].Name != tt.want[i].Name {
					t.Errorf("DecodeArrayData() got[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
