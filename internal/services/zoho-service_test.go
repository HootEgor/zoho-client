package services

import (
	"encoding/json"
	"strings"
	"testing"
	"zohoclient/entity"
)

// The update payload carries the new rows followed by a delete for each row the order held.
func TestReplacingRows(t *testing.T) {
	tests := []struct {
		name  string
		stale []string
		want  string
	}{
		{"no stale rows", nil,
			`[{"DiscountP":0,"GetDiscount":0,"List_Price":8.8513,"Product_Name":{"id":"739178000000000001","name":""},"Quantity":2,"Total":17.7026}]`},
		{"stale rows deleted", []string{"739178000073293400", "739178000073293401"},
			`[{"DiscountP":0,"GetDiscount":0,"List_Price":8.8513,"Product_Name":{"id":"739178000000000001","name":""},"Quantity":2,"Total":17.7026},` +
				`{"_delete":null,"id":"739178000073293400"},{"_delete":null,"id":"739178000073293401"}]`},
	}
	order := entity.ZohoOrder{
		Subject:  "Order #18313",
		Currency: "EUR",
		OrderedItems: []entity.OrderedItem{{
			Product:   entity.ZohoProduct{ID: "739178000000000001"},
			Quantity:  2,
			ListPrice: 8.8513,
			Total:     17.7026,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := replacingRows(order, tt.stale)
			if err != nil {
				t.Fatalf("replacingRows() error = %v", err)
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got := string(raw)
			if !strings.Contains(got, `"Ordered_Items":`+tt.want) {
				t.Errorf("Ordered_Items not as expected:\n%s\nwant %s", got, tt.want)
			}
			if strings.Contains(got, `"Status"`) {
				t.Errorf("empty Status sent: %s", got)
			}
			if !strings.Contains(got, `"Subject":"Order #18313"`) || !strings.Contains(got, `"Currency":"EUR"`) {
				t.Errorf("order fields lost: %s", got)
			}
		})
	}
}
