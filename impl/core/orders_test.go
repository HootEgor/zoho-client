package core

import (
	"math"
	"testing"
	"zohoclient/entity"
)

func TestRoundInt(t *testing.T) {
	tests := []struct {
		name     string
		value    int64
		expected float64
	}{
		{"zero", 0, 0.0},
		{"one cent", 1, 0.01},
		{"one dollar", 100, 1.0},
		{"negative value", -150, -1.5},
		{"large value", 1234567, 12345.67},
		{"typical price", 9999, 99.99},
		{"half cent rounds down", 50, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := roundInt(tt.value)
			if result != tt.expected {
				t.Errorf("roundInt(%d) = %v, want %v", tt.value, result, tt.expected)
			}
		})
	}
}

func TestRoundFloat(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		expected float64
	}{
		{"zero", 0.0, 0.0},
		{"positive whole number", 5.0, 5.0},
		{"negative converted to positive", -5.0, 5.0},
		{"rounds down at .004", 1.234, 1.23},
		{"rounds up at .005", 1.235, 1.24},
		{"rounds up at .006", 1.236, 1.24},
		{"many decimal places rounds", 3.14159, 3.14},
		{"negative rounds to positive", -2.567, 2.57},
		{"small negative", -0.01, 0.01},
		{"precision edge case", 0.995, 1.0},
		{"precision edge case 2", 0.994, 0.99},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := roundFloat(tt.value)
			if result != tt.expected {
				t.Errorf("roundFloat(%v) = %v, want %v", tt.value, result, tt.expected)
			}
		})
	}
}

func TestHasEmptyZohoID(t *testing.T) {
	tests := []struct {
		name      string
		products  []*entity.LineItem
		expectErr bool
	}{
		{
			name:      "empty slice",
			products:  []*entity.LineItem{},
			expectErr: false,
		},
		{
			name:      "nil slice",
			products:  nil,
			expectErr: false,
		},
		{
			name: "all products have zoho_id",
			products: []*entity.LineItem{
				{Id: 1, Name: "Product 1", ZohoId: "123"},
				{Id: 2, Name: "Product 2", ZohoId: "456"},
			},
			expectErr: false,
		},
		{
			name: "first product missing zoho_id",
			products: []*entity.LineItem{
				{Id: 1, Name: "Product 1", ZohoId: ""},
				{Id: 2, Name: "Product 2", ZohoId: "456"},
			},
			expectErr: true,
		},
		{
			name: "last product missing zoho_id",
			products: []*entity.LineItem{
				{Id: 1, Name: "Product 1", ZohoId: "123"},
				{Id: 2, Name: "Product 2", ZohoId: ""},
			},
			expectErr: true,
		},
		{
			name: "middle product missing zoho_id",
			products: []*entity.LineItem{
				{Id: 1, Name: "Product 1", ZohoId: "123"},
				{Id: 2, Name: "Product 2", ZohoId: ""},
				{Id: 3, Name: "Product 3", ZohoId: "789"},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := hasEmptyZohoID(tt.products)
			if (err != nil) != tt.expectErr {
				t.Errorf("hasEmptyZohoID() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestHasEmptyUid(t *testing.T) {
	tests := []struct {
		name      string
		products  []*entity.LineItem
		expectErr bool
	}{
		{
			name:      "empty slice",
			products:  []*entity.LineItem{},
			expectErr: false,
		},
		{
			name:      "nil slice",
			products:  nil,
			expectErr: false,
		},
		{
			name: "all products have uid",
			products: []*entity.LineItem{
				{Id: 1, Name: "Product 1", Uid: "uid-1"},
				{Id: 2, Name: "Product 2", Uid: "uid-2"},
			},
			expectErr: false,
		},
		{
			name: "product missing uid",
			products: []*entity.LineItem{
				{Id: 1, Name: "Product 1", Uid: "uid-1"},
				{Id: 2, Name: "Product 2", Uid: ""},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := hasEmptyUid(tt.products)
			if (err != nil) != tt.expectErr {
				t.Errorf("hasEmptyUid() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestBuildZohoOrder_Chunking(t *testing.T) {
	// Create a minimal Core for testing
	core := &Core{
		statuses: map[int]string{1: "Confirmed"},
	}

	tests := []struct {
		name               string
		itemCount          int
		expectedMainItems  int
		expectedChunkCount int
		expectedLastChunk  int
	}{
		{
			name:               "less than chunk size",
			itemCount:          50,
			expectedMainItems:  50,
			expectedChunkCount: 0,
			expectedLastChunk:  0,
		},
		{
			name:               "exactly chunk size",
			itemCount:          100,
			expectedMainItems:  100,
			expectedChunkCount: 0,
			expectedLastChunk:  0,
		},
		{
			name:               "one over chunk size",
			itemCount:          101,
			expectedMainItems:  100,
			expectedChunkCount: 1,
			expectedLastChunk:  1,
		},
		{
			name:               "two full chunks",
			itemCount:          200,
			expectedMainItems:  100,
			expectedChunkCount: 1,
			expectedLastChunk:  100,
		},
		{
			name:               "two chunks plus remainder",
			itemCount:          250,
			expectedMainItems:  100,
			expectedChunkCount: 2,
			expectedLastChunk:  50,
		},
		{
			name:               "three full chunks",
			itemCount:          300,
			expectedMainItems:  100,
			expectedChunkCount: 2,
			expectedLastChunk:  100,
		},
		{
			name:               "three chunks plus remainder",
			itemCount:          350,
			expectedMainItems:  100,
			expectedChunkCount: 3,
			expectedLastChunk:  50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create line items
			lineItems := make([]*entity.LineItem, tt.itemCount)
			for i := 0; i < tt.itemCount; i++ {
				lineItems[i] = &entity.LineItem{
					Id:     int64(i + 1),
					Name:   "Product",
					ZohoId: "zoho-id",
					Qty:    1,
					Price:  1000,
				}
			}

			order := &entity.CheckoutParams{
				OrderId:   123,
				Total:     int64(tt.itemCount * 1000),
				Currency:  "PLN",
				StatusId:  1,
				LineItems: lineItems,
				ClientDetails: &entity.ClientDetails{
					FirstName: "Test",
					LastName:  "User",
					Country:   "Poland",
					ZipCode:   "00-001",
				},
			}

			zohoOrder, chunkedItems := core.buildZohoOrder(order, "contact-123")

			// Check main order items
			if len(zohoOrder.OrderedItems) != tt.expectedMainItems {
				t.Errorf("Expected %d main items, got %d", tt.expectedMainItems, len(zohoOrder.OrderedItems))
			}

			// Check chunk count
			if len(chunkedItems) != tt.expectedChunkCount {
				t.Errorf("Expected %d chunks, got %d", tt.expectedChunkCount, len(chunkedItems))
			}

			// Check last chunk size if there are chunks
			if tt.expectedChunkCount > 0 {
				lastChunkSize := len(chunkedItems[len(chunkedItems)-1])
				if lastChunkSize != tt.expectedLastChunk {
					t.Errorf("Expected last chunk size %d, got %d", tt.expectedLastChunk, lastChunkSize)
				}
			}

			// Verify total item count
			totalItems := len(zohoOrder.OrderedItems)
			for _, chunk := range chunkedItems {
				totalItems += len(chunk)
			}
			if totalItems != tt.itemCount {
				t.Errorf("Total items mismatch: expected %d, got %d", tt.itemCount, totalItems)
			}
		})
	}
}

func TestCalculateDiscountPercent(t *testing.T) {
	core := &Core{}

	tests := []struct {
		name     string
		items    []entity.ApiOrderedItem
		expected float64
	}{
		{
			name:     "empty items",
			items:    []entity.ApiOrderedItem{},
			expected: 0,
		},
		{
			name: "no discount - full price equals total",
			items: []entity.ApiOrderedItem{
				{Price: 100.0, Quantity: 1, Total: 100.0},
			},
			expected: 0,
		},
		{
			name: "10% discount",
			items: []entity.ApiOrderedItem{
				{Price: 100.0, Quantity: 1, Total: 90.0},
			},
			expected: 0.1,
		},
		{
			name: "25% discount",
			items: []entity.ApiOrderedItem{
				{Price: 100.0, Quantity: 1, Total: 75.0},
			},
			expected: 0.25,
		},
		{
			name: "50% discount",
			items: []entity.ApiOrderedItem{
				{Price: 100.0, Quantity: 2, Total: 100.0}, // Full would be 200, so 50% off
			},
			expected: 0.5,
		},
		{
			name: "multiple items with mixed discounts",
			items: []entity.ApiOrderedItem{
				{Price: 100.0, Quantity: 1, Total: 90.0}, // 10% off
				{Price: 50.0, Quantity: 2, Total: 90.0},  // 10% off (full = 100)
			},
			expected: 0.1, // (90+90) / (100+100) = 180/200 = 0.9, so discount = 0.1
		},
		{
			name: "100% discount (free)",
			items: []entity.ApiOrderedItem{
				{Price: 100.0, Quantity: 1, Total: 0.0},
			},
			expected: 1.0,
		},
		{
			name: "negative discount (shouldn't happen but handled)",
			items: []entity.ApiOrderedItem{
				{Price: 100.0, Quantity: 1, Total: 110.0}, // More than full price
			},
			expected: -0.1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := core.calculateDiscountPercent(tt.items)
			// Use small epsilon for floating point comparison
			if math.Abs(result-tt.expected) > 0.0001 {
				t.Errorf("calculateDiscountPercent() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCalculateDiscountPercent_ZeroFullTotal(t *testing.T) {
	core := &Core{}

	// Edge case: all items have zero price
	items := []entity.ApiOrderedItem{
		{Price: 0.0, Quantity: 1, Total: 0.0},
	}

	result := core.calculateDiscountPercent(items)
	if result != 0 {
		t.Errorf("calculateDiscountPercent() with zero prices = %v, want 0", result)
	}
}
