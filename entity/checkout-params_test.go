package entity

import (
	"testing"
)

func TestTaxRate(t *testing.T) {
	tests := []struct {
		name     string
		total    float64
		subTotal float64
		taxValue float64
		shipping float64
		expected float64
	}{
		{
			name:     "zero tax",
			total:    100.00,
			taxValue: 0,
			shipping: 0,
			expected: 0,
		},
		{
			name:     "tax equals total - edge case",
			total:    10.00,
			taxValue: 10.00,
			shipping: 0,
			expected: 0,
		},
		{
			name:     "tax greater than total - edge case",
			total:    10.00,
			taxValue: 15.00,
			shipping: 0,
			expected: 0,
		},
		{
			name:     "23% VAT standard calculation",
			total:    123.00,
			subTotal: 100.00,
			taxValue: 23.00,
			shipping: 0,
			expected: 23.0, // 23 / 100 * 100 = 23%
		},
		{
			name:     "8% reduced VAT",
			total:    108.00,
			subTotal: 100.00,
			taxValue: 8.00,
			shipping: 0,
			expected: 8.0, // 8 / 100 * 100 = 8%
		},
		{
			name:     "with shipping deducted",
			total:    133.00, // includes 10 shipping
			subTotal: 100.00,
			taxValue: 23.00,
			shipping: 10.00,
			expected: 23.0, // 23 / 100 * 100 = 23%
		},
		{
			name:     "5% VAT",
			total:    105.00,
			subTotal: 100.00,
			taxValue: 5.00,
			shipping: 0,
			expected: 5.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CheckoutParams{
				Total:    tt.total,
				SubTotal: tt.subTotal,
				TaxValue: tt.taxValue,
				Shipping: tt.shipping,
			}
			result := c.TaxRate()
			if diff := result - tt.expected; diff > 0.01 || diff < -0.01 {
				t.Errorf("TaxRate() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCouponIsPreTax(t *testing.T) {
	// A line whose per-unit tax is 23% of its price fixes the nominal VAT rate.
	line := func(price float64) []*LineItem {
		return []*LineItem{{Price: price, Tax: price * 0.23, Qty: 1, Total: price}}
	}

	tests := []struct {
		name     string
		subTotal float64
		taxValue float64
		coupon   float64
		lines    []*LineItem
		want     bool
	}{
		{name: "no coupon", subTotal: 1000, taxValue: 230, coupon: 0, lines: line(100), want: true},
		{name: "pre-tax coupon (tax on reduced base)", subTotal: 1000, taxValue: 207, coupon: -100, lines: line(100), want: true},
		{name: "post-tax coupon (tax on full base)", subTotal: 1000, taxValue: 230, coupon: -100, lines: line(100), want: false},
		{name: "no usable line rate falls back to pre-tax", subTotal: 1000, taxValue: 230, coupon: -100, lines: []*LineItem{{Total: 1000}}, want: true},
		{
			// Real order #16939: tax_value 97.2357 == 422.764 * 23% -> full base -> post-tax.
			name: "order 16939 post-tax coupon", subTotal: 422.764, taxValue: 97.2357, coupon: -42.2764,
			lines: []*LineItem{{Price: 52.8455, Tax: 12.1545, Qty: 1, Total: 52.8455}}, want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CheckoutParams{SubTotal: tt.subTotal, TaxValue: tt.taxValue, Coupon: tt.coupon, LineItems: tt.lines}
			if got := c.CouponIsPreTax(); got != tt.want {
				t.Errorf("CouponIsPreTax() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TaxRate must use the full subtotal for a post-tax coupon and the reduced subtotal for a
// pre-tax coupon, so the VAT% sent to Zoho matches how OpenCart charged it.
func TestTaxRateWithCoupon(t *testing.T) {
	postTax := &CheckoutParams{
		SubTotal: 422.764, TaxValue: 97.2357, Coupon: -42.2764,
		LineItems: []*LineItem{{Price: 52.8455, Tax: 12.1545, Qty: 1, Total: 52.8455}},
	}
	if got := postTax.TaxRate(); got < 22.99 || got > 23.01 {
		t.Errorf("post-tax coupon TaxRate() = %v, want ~23", got)
	}

	preTax := &CheckoutParams{
		SubTotal: 1000, TaxValue: 207, Coupon: -100,
		LineItems: []*LineItem{{Price: 100, Tax: 23, Qty: 10, Total: 1000}},
	}
	if got := preTax.TaxRate(); got < 22.99 || got > 23.01 {
		t.Errorf("pre-tax coupon TaxRate() = %v, want ~23", got)
	}
}

func TestDiscountPercent(t *testing.T) {
	tests := []struct {
		name            string
		total           float64
		taxValue        float64
		shipping        float64
		lineItems       []*LineItem
		expectedValue   float64
		expectedPercent float64
	}{
		{
			name:            "no discount",
			total:           123.00, // 100 base + 23 tax
			taxValue:        23.00,
			shipping:        0,
			lineItems:       []*LineItem{{Total: 100.00}},
			expectedValue:   0,
			expectedPercent: 0,
		},
		{
			name:            "10% discount",
			total:           113.00, // 90 after discount + 23 tax
			taxValue:        23.00,
			shipping:        0,
			lineItems:       []*LineItem{{Total: 100.00}},
			expectedValue:   10.0,
			expectedPercent: 10.0,
		},
		{
			name:            "20% discount with shipping",
			total:           103.00, // 80 after discount + 23 tax + 0 shipping (shipping not in lineItems)
			taxValue:        23.00,
			shipping:        0,
			lineItems:       []*LineItem{{Total: 100.00}},
			expectedValue:   20.0,
			expectedPercent: 20.0,
		},
		{
			name:            "discount with shipping excluded",
			total:           123.00, // 90 after discount + 23 tax + 10 shipping
			taxValue:        23.00,
			shipping:        10.00,
			lineItems:       []*LineItem{{Total: 100.00}},
			expectedValue:   10.0,
			expectedPercent: 10.0,
		},
		{
			name:            "multiple items with discount",
			total:           223.00, // 180 after discount + 43 tax
			taxValue:        43.00,
			shipping:        0,
			lineItems:       []*LineItem{{Total: 100.00}, {Total: 100.00}},
			expectedValue:   20.0,
			expectedPercent: 10.0, // (200 - 180) / 200 * 100 = 10%
		},
		{
			name:            "empty line items",
			total:           100.00,
			taxValue:        0,
			shipping:        0,
			lineItems:       []*LineItem{},
			expectedValue:   0,
			expectedPercent: 0,
		},
		{
			name:            "zero base total",
			total:           100.00,
			taxValue:        0,
			shipping:        0,
			lineItems:       []*LineItem{{Total: 0}},
			expectedValue:   0,
			expectedPercent: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CheckoutParams{
				Total:     tt.total,
				TaxValue:  tt.taxValue,
				Shipping:  tt.shipping,
				LineItems: tt.lineItems,
			}
			discountValue, discountPercent := c.GetDiscount()
			if diff := discountValue - tt.expectedValue; diff > 0.01 || diff < -0.01 {
				t.Errorf("DiscountPercent() value = %v, want %v", discountValue, tt.expectedValue)
			}
			if diff := discountPercent - tt.expectedPercent; diff > 0.01 || diff < -0.01 {
				t.Errorf("DiscountPercent() percent = %v, want %v", discountPercent, tt.expectedPercent)
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

func TestTrimSpaces(t *testing.T) {
	tests := []struct {
		name     string
		input    ClientDetails
		expected ClientDetails
	}{
		{
			name: "trims leading and trailing spaces",
			input: ClientDetails{
				FirstName: " John ",
				LastName:  " Doe ",
				Email:     " john@example.com ",
				Phone:     " +48123456789 ",
				Country:   " Poland ",
				ZipCode:   " 00-001 ",
				City:      " Warsaw ",
				Street:    " Main St 1 ",
			},
			expected: ClientDetails{
				FirstName: "John",
				LastName:  "Doe",
				Email:     "john@example.com",
				Phone:     "+48123456789",
				Country:   "Poland",
				ZipCode:   "00-001",
				City:      "Warsaw",
				Street:    "Main St 1",
			},
		},
		{
			name: "handles empty strings",
			input: ClientDetails{
				FirstName: "",
				LastName:  "",
			},
			expected: ClientDetails{
				FirstName: "",
				LastName:  "",
			},
		},
		{
			name: "preserves internal spaces",
			input: ClientDetails{
				FirstName: " Mary Ann ",
				Street:    " 123 Main Street ",
			},
			expected: ClientDetails{
				FirstName: "Mary Ann",
				Street:    "123 Main Street",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.input
			c.TrimSpaces()
			if c.FirstName != tt.expected.FirstName {
				t.Errorf("FirstName = %q, want %q", c.FirstName, tt.expected.FirstName)
			}
			if c.LastName != tt.expected.LastName {
				t.Errorf("LastName = %q, want %q", c.LastName, tt.expected.LastName)
			}
			if c.Email != tt.expected.Email {
				t.Errorf("Email = %q, want %q", c.Email, tt.expected.Email)
			}
			if c.Phone != tt.expected.Phone {
				t.Errorf("Phone = %q, want %q", c.Phone, tt.expected.Phone)
			}
			if c.Country != tt.expected.Country {
				t.Errorf("Country = %q, want %q", c.Country, tt.expected.Country)
			}
			if c.ZipCode != tt.expected.ZipCode {
				t.Errorf("ZipCode = %q, want %q", c.ZipCode, tt.expected.ZipCode)
			}
			if c.City != tt.expected.City {
				t.Errorf("City = %q, want %q", c.City, tt.expected.City)
			}
			if c.Street != tt.expected.Street {
				t.Errorf("Street = %q, want %q", c.Street, tt.expected.Street)
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
		{"mixed alphanumeric", "AB12CD34EF", "01-234"},
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

func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		params    CheckoutParams
		expectErr bool
	}{
		{
			name: "valid order",
			params: CheckoutParams{
				LineItems:     []*LineItem{{Name: "Product", Qty: 1, Price: 10.00}},
				ClientDetails: &ClientDetails{FirstName: "John", LastName: "Doe"},
			},
			expectErr: false,
		},
		{
			name: "missing line items",
			params: CheckoutParams{
				LineItems:     []*LineItem{},
				ClientDetails: &ClientDetails{FirstName: "John", LastName: "Doe"},
			},
			expectErr: true,
		},
		{
			name: "nil client details",
			params: CheckoutParams{
				LineItems:     []*LineItem{{Name: "Product", Qty: 1, Price: 10.00}},
				ClientDetails: nil,
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.Validate()
			if (err != nil) != tt.expectErr {
				t.Errorf("Validate() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}
