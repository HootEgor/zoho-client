package entity

import (
	"testing"
)

func TestItemsTotal(t *testing.T) {
	tests := []struct {
		name      string
		lineItems []*LineItem
		expected  int64
	}{
		{
			name:      "empty items",
			lineItems: []*LineItem{},
			expected:  0,
		},
		{
			name:      "nil items",
			lineItems: nil,
			expected:  0,
		},
		{
			name: "single item no discount",
			lineItems: []*LineItem{
				{Qty: 1, Price: 1000, Discount: 0},
			},
			expected: 1000,
		},
		{
			name: "single item with discount",
			lineItems: []*LineItem{
				{Qty: 1, Price: 1000, Discount: 100},
			},
			expected: 900,
		},
		{
			name: "multiple items",
			lineItems: []*LineItem{
				{Qty: 2, Price: 1000, Discount: 0},
				{Qty: 1, Price: 500, Discount: 50},
			},
			expected: 2450, // 2000 + 450
		},
		{
			name: "quantity multiplied correctly",
			lineItems: []*LineItem{
				{Qty: 5, Price: 200, Discount: 0},
			},
			expected: 1000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CheckoutParams{LineItems: tt.lineItems}
			result := c.ItemsTotal()
			if result != tt.expected {
				t.Errorf("ItemsTotal() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func TestValidateTotal(t *testing.T) {
	tests := []struct {
		name      string
		total     int64
		lineItems []*LineItem
		expectErr bool
	}{
		{
			name:  "matching totals",
			total: 1000,
			lineItems: []*LineItem{
				{Qty: 1, Price: 1000, Discount: 0},
			},
			expectErr: false,
		},
		{
			name:  "total too high",
			total: 1100,
			lineItems: []*LineItem{
				{Qty: 1, Price: 1000, Discount: 0},
			},
			expectErr: true,
		},
		{
			name:  "total too low",
			total: 900,
			lineItems: []*LineItem{
				{Qty: 1, Price: 1000, Discount: 0},
			},
			expectErr: true,
		},
		{
			name:  "complex matching",
			total: 2450,
			lineItems: []*LineItem{
				{Qty: 2, Price: 1000, Discount: 0},
				{Qty: 1, Price: 500, Discount: 50},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CheckoutParams{Total: tt.total, LineItems: tt.lineItems}
			err := c.ValidateTotal()
			if (err != nil) != tt.expectErr {
				t.Errorf("ValidateTotal() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestTaxRate(t *testing.T) {
	tests := []struct {
		name     string
		total    int64
		taxValue int64
		shipping int64
		expected int
	}{
		{
			name:     "zero tax",
			total:    10000,
			taxValue: 0,
			shipping: 0,
			expected: 0,
		},
		{
			name:     "tax equals total - edge case",
			total:    1000,
			taxValue: 1000,
			shipping: 0,
			expected: 0,
		},
		{
			name:     "tax greater than total - edge case",
			total:    1000,
			taxValue: 1500,
			shipping: 0,
			expected: 0,
		},
		{
			name:     "23% VAT standard calculation",
			total:    12300,
			taxValue: 2300,
			shipping: 0,
			expected: 23, // 2300 / (12300 - 2300) = 0.23
		},
		{
			name:     "8% reduced VAT",
			total:    10800,
			taxValue: 800,
			shipping: 0,
			expected: 8, // 800 / (10800 - 800) = 0.08
		},
		{
			name:     "with shipping deducted",
			total:    13300, // includes 1000 shipping
			taxValue: 2300,
			shipping: 1000,
			expected: 23, // 2300 / (13300 - 1000 - 2300) = 2300/10000 = 0.23
		},
		{
			name:     "5% VAT",
			total:    10500,
			taxValue: 500,
			shipping: 0,
			expected: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CheckoutParams{
				Total:    tt.total,
				TaxValue: tt.taxValue,
				Shipping: tt.shipping,
			}
			result := c.TaxRate()
			if result != tt.expected {
				t.Errorf("TaxRate() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func TestRecalcWithDiscount(t *testing.T) {
	tests := []struct {
		name              string
		total             int64
		shipping          int64
		lineItems         []*LineItem
		expectedDiscountP float64
		expectedTotal     int64
	}{
		{
			name:     "no discount needed - totals match",
			total:    1000,
			shipping: 0,
			lineItems: []*LineItem{
				{Qty: 1, Price: 1000, Discount: 0},
			},
			expectedDiscountP: 0,
			expectedTotal:     1000,
		},
		{
			name:     "10% discount",
			total:    900,
			shipping: 0,
			lineItems: []*LineItem{
				{Qty: 1, Price: 1000, Discount: 0},
			},
			expectedDiscountP: 10,
			expectedTotal:     900,
		},
		{
			name:     "shipping excluded from discount",
			total:    1100, // 900 product + 200 shipping
			shipping: 200,
			lineItems: []*LineItem{
				{Qty: 1, Price: 1000, Discount: 0, Shipping: false},
				{Qty: 1, Price: 200, Discount: 0, Shipping: true},
			},
			expectedDiscountP: 10, // 10% on product only
			expectedTotal:     1100,
		},
		{
			name:     "multiple items with discount",
			total:    1800, // 20% off from 2250
			shipping: 0,
			lineItems: []*LineItem{
				{Qty: 1, Price: 1000, Discount: 0},
				{Qty: 1, Price: 1250, Discount: 0},
			},
			expectedDiscountP: 20,
			expectedTotal:     1800,
		},
		{
			name:              "empty line items",
			total:             1000,
			shipping:          0,
			lineItems:         []*LineItem{},
			expectedDiscountP: 0,
			expectedTotal:     1000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CheckoutParams{
				Total:     tt.total,
				Shipping:  tt.shipping,
				LineItems: tt.lineItems,
			}
			c.RecalcWithDiscount()

			// Check discount percentage (with small tolerance for floating point)
			if diff := c.DiscountP - tt.expectedDiscountP; diff > 0.01 || diff < -0.01 {
				t.Errorf("DiscountP = %v, want %v", c.DiscountP, tt.expectedDiscountP)
			}

			// Check that total matches after recalculation
			if c.ItemsTotal() != tt.expectedTotal {
				t.Errorf("ItemsTotal() after RecalcWithDiscount = %d, want %d", c.ItemsTotal(), tt.expectedTotal)
			}
		})
	}
}

func TestIsB2B(t *testing.T) {
	tests := []struct {
		name     string
		groupId  int64
		expected bool
	}{
		{"group 0 is not B2B", 0, false},
		{"group 1 is not B2B", 1, false},
		{"group 5 is not B2B", 5, false},
		{"group 6 is B2B", 6, true},
		{"group 7 is B2B", 7, true},
		{"group 8 is not B2B", 8, false},
		{"group 16 is B2B", 16, true},
		{"group 17 is not B2B", 17, false},
		{"group 18 is B2B", 18, true},
		{"group 19 is B2B", 19, true},
		{"group 20 is not B2B", 20, false},
		{"group 100 is not B2B", 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClientDetails{GroupId: tt.groupId}
			result := c.IsB2B()
			if result != tt.expected {
				t.Errorf("IsB2B() for group %d = %v, want %v", tt.groupId, result, tt.expected)
			}
		})
	}
}

func TestCountryCode(t *testing.T) {
	tests := []struct {
		name     string
		country  string
		expected string
	}{
		{"empty string", "", ""},
		{"2-letter code passthrough", "PL", "PL"},
		{"2-letter code lowercase passthrough", "pl", "pl"},
		{"full name Poland", "Poland", "PL"},
		{"full name Germany", "Germany", "DE"},
		{"full name France", "France", "FR"},
		{"full name United States", "United States", "US"},
		{"invalid country", "InvalidCountryName", ""},
		{"Polska in Polish", "Polska", "PL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClientDetails{Country: tt.country}
			result := c.CountryCode()
			if result != tt.expected {
				t.Errorf("CountryCode() for %q = %q, want %q", tt.country, result, tt.expected)
			}
		})
	}
}

func TestNormalizeZipCode(t *testing.T) {
	tests := []struct {
		name     string
		zipCode  string
		expected string
	}{
		{"already normalized", "00-001", "00-001"},
		{"already normalized 2", "12-345", "12-345"},
		{"without dash", "00001", "00-001"},
		{"without dash 2", "12345", "12-345"},
		{"short - needs padding", "123", "00-123"},
		{"short 2 digits", "12", "00-012"},
		{"single digit", "1", "00-001"},
		{"empty - all zeros", "", "00-000"},
		{"too long - truncates", "123456", "12-345"},
		{"with spaces", "12 345", "12-345"},
		{"with letters filtered", "12-ABC-345", "12-345"},
		{"mixed alphanumeric", "AB12CD34EF", "12-340"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClientDetails{ZipCode: tt.zipCode}
			result := c.NormalizeZipCode()
			if result != tt.expected {
				t.Errorf("NormalizeZipCode() for %q = %q, want %q", tt.zipCode, result, tt.expected)
			}
		})
	}
}

func TestParseTaxId(t *testing.T) {
	tests := []struct {
		name      string
		fieldId   string
		raw       string
		expected  string
		expectErr bool
	}{
		{
			name:      "empty field ID",
			fieldId:   "",
			raw:       `{"2":"DE362155758"}`,
			expected:  "",
			expectErr: false,
		},
		{
			name:      "empty raw",
			fieldId:   "2",
			raw:       "",
			expected:  "",
			expectErr: false,
		},
		{
			name:      "valid JSON with matching field",
			fieldId:   "2",
			raw:       `{"2":"DE362155758"}`,
			expected:  "DE362155758",
			expectErr: false,
		},
		{
			name:      "valid JSON with non-matching field",
			fieldId:   "3",
			raw:       `{"2":"DE362155758"}`,
			expected:  "",
			expectErr: false,
		},
		{
			name:      "invalid JSON",
			fieldId:   "2",
			raw:       `not valid json`,
			expected:  "",
			expectErr: true,
		},
		{
			name:      "multiple fields",
			fieldId:   "3",
			raw:       `{"2":"DE362155758","3":"PL1234567890"}`,
			expected:  "PL1234567890",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClientDetails{}
			err := c.ParseTaxId(tt.fieldId, tt.raw)
			if (err != nil) != tt.expectErr {
				t.Errorf("ParseTaxId() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if c.TaxId != tt.expected {
				t.Errorf("ParseTaxId() TaxId = %q, want %q", c.TaxId, tt.expected)
			}
		})
	}
}

func TestShippingLineItem(t *testing.T) {
	tests := []struct {
		name         string
		title        string
		amount       int64
		expectedName string
	}{
		{
			name:         "empty title uses default",
			title:        "",
			amount:       1000,
			expectedName: "Zwrot kosztów transportu towarów",
		},
		{
			name:         "custom title is wrapped",
			title:        "DHL",
			amount:       1500,
			expectedName: "Zwrot kosztów transportu towarów (DHL)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := ShippingLineItem(tt.title, tt.amount)

			if item.Name != tt.expectedName {
				t.Errorf("Name = %q, want %q", item.Name, tt.expectedName)
			}
			if item.Uid != ShippingItemUid {
				t.Errorf("Uid = %q, want %q", item.Uid, ShippingItemUid)
			}
			if item.Qty != 1 {
				t.Errorf("Qty = %d, want 1", item.Qty)
			}
			if item.Price != tt.amount {
				t.Errorf("Price = %d, want %d", item.Price, tt.amount)
			}
			if !item.Shipping {
				t.Error("Shipping flag should be true")
			}
		})
	}
}
