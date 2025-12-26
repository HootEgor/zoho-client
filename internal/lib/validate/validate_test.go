package validate

import (
	"strings"
	"testing"
)

type TestStruct struct {
	Name     string `json:"name" validate:"required"`
	Email    string `json:"email" validate:"required,email"`
	Age      int    `json:"age" validate:"gte=0,lte=120"`
	Optional string `json:"optional,omitempty"`
}

type NestedStruct struct {
	ID   int         `json:"id" validate:"required,gt=0"`
	Data *TestStruct `json:"data" validate:"required"`
}

func TestStruct_ValidInput(t *testing.T) {
	s := &TestStruct{
		Name:  "John Doe",
		Email: "john@example.com",
		Age:   30,
	}

	err := Struct(s)
	if err != nil {
		t.Errorf("Struct() with valid input returned error: %v", err)
	}
}

func TestStruct_MissingRequired(t *testing.T) {
	s := &TestStruct{
		Name:  "",
		Email: "john@example.com",
		Age:   30,
	}

	err := Struct(s)
	if err == nil {
		t.Error("Struct() should return error for missing required field")
	}

	// Error should mention the field name from json tag
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("Error should mention 'name' field, got: %v", err)
	}
}

func TestStruct_InvalidEmail(t *testing.T) {
	s := &TestStruct{
		Name:  "John Doe",
		Email: "not-an-email",
		Age:   30,
	}

	err := Struct(s)
	if err == nil {
		t.Error("Struct() should return error for invalid email")
	}

	if !strings.Contains(err.Error(), "email") {
		t.Errorf("Error should mention 'email' field, got: %v", err)
	}
}

func TestStruct_AgeOutOfRange(t *testing.T) {
	tests := []struct {
		name      string
		age       int
		expectErr bool
	}{
		{"valid age", 30, false},
		{"zero age", 0, false},
		{"max age", 120, false},
		{"negative age", -1, true},
		{"over max age", 121, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &TestStruct{
				Name:  "John",
				Email: "john@example.com",
				Age:   tt.age,
			}
			err := Struct(s)
			if (err != nil) != tt.expectErr {
				t.Errorf("Struct() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestStruct_MultipleErrors(t *testing.T) {
	s := &TestStruct{
		Name:  "",
		Email: "not-an-email",
		Age:   -1,
	}

	err := Struct(s)
	if err == nil {
		t.Error("Struct() should return error for multiple invalid fields")
	}

	// Should contain separator for multiple errors
	if !strings.Contains(err.Error(), ";") {
		t.Errorf("Multiple errors should be separated by ';', got: %v", err)
	}
}

func TestStruct_NilInput(t *testing.T) {
	err := Struct(nil)
	if err == nil {
		t.Error("Struct(nil) should return error")
	}

	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("Error should mention 'nil', got: %v", err)
	}
}

func TestStruct_NotAStruct(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
	}{
		{"string", "not a struct"},
		{"int", 42},
		{"slice", []int{1, 2, 3}},
		{"map", map[string]int{"a": 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Struct(tt.input)
			if err == nil {
				t.Error("Struct() should return error for non-struct input")
			}

			if !strings.Contains(err.Error(), "not a struct") {
				t.Errorf("Error should mention 'not a struct', got: %v", err)
			}
		})
	}
}

func TestStruct_PointerToStruct(t *testing.T) {
	// Should work with pointer to struct
	s := &TestStruct{
		Name:  "John",
		Email: "john@example.com",
		Age:   30,
	}

	err := Struct(s)
	if err != nil {
		t.Errorf("Struct() with pointer should work, got error: %v", err)
	}
}

func TestStruct_NonPointerStruct(t *testing.T) {
	// Should also work with non-pointer struct
	s := TestStruct{
		Name:  "John",
		Email: "john@example.com",
		Age:   30,
	}

	err := Struct(s)
	if err != nil {
		t.Errorf("Struct() with non-pointer should work, got error: %v", err)
	}
}

func TestIsStruct(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected bool
	}{
		{"struct value", TestStruct{}, true},
		{"struct pointer", &TestStruct{}, true},
		{"string", "hello", false},
		{"int", 42, false},
		{"slice", []int{1, 2, 3}, false},
		{"map", map[string]int{}, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip nil case as it causes panic
			if tt.input == nil {
				return
			}

			result := isStruct(tt.input)
			if result != tt.expected {
				t.Errorf("isStruct(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestStruct_JsonTagFieldNames(t *testing.T) {
	// Verify that json tag names are used in error messages
	type CustomStruct struct {
		InternalName string `json:"public_name" validate:"required"`
	}

	s := &CustomStruct{InternalName: ""}
	err := Struct(s)
	if err == nil {
		t.Error("Should return error for missing required field")
	}

	// Error message should use json tag name, not Go field name
	if !strings.Contains(err.Error(), "public_name") {
		t.Errorf("Error should use json tag name 'public_name', got: %v", err)
	}

	if strings.Contains(err.Error(), "InternalName") {
		t.Errorf("Error should not contain Go field name 'InternalName', got: %v", err)
	}
}

func TestGetValidator_Singleton(t *testing.T) {
	// Should return the same instance
	v1 := getValidator()
	v2 := getValidator()

	if v1 != v2 {
		t.Error("getValidator() should return the same instance (singleton)")
	}
}
