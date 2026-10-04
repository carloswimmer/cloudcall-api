package contact_test

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"cloudcall/internal/contact"
)

func TestValidatePhone(t *testing.T) {
	cases := []struct {
		phone string
		ok    bool
	}{
		{"+442071838750", true},
		{"+12345678", true},          // 8 digits, minimum
		{"+123456789012345", true},   // 15 digits, maximum
		{"+0123", false},             // leading zero, too short
		{"+01234567890", false},      // leading zero
		{"442071838750", false},      // missing plus
		{"+1234567", false},          // too short (7)
		{"+1234567890123456", false}, // too long (16)
		{"+44 2071838750", false},    // spaces
		{"+44abc1838750", false},     // letters
		{"", false},
	}
	for _, c := range cases {
		err := contact.ValidatePhone(c.phone)
		if c.ok && err != nil {
			t.Errorf("%q: unexpected error %v", c.phone, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%q: expected error", c.phone)
		}
	}
}

func TestValidateName(t *testing.T) {
	if err := contact.ValidateName("Ada Lovelace"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("a", 121)} {
		if err := contact.ValidateName(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestValidateEmail(t *testing.T) {
	for _, ok := range []string{"", "ada@example.com", "a.b@mail.example.org"} {
		if err := contact.ValidateEmail(ok); err != nil {
			t.Errorf("%q: unexpected error %v", ok, err)
		}
	}
	for _, bad := range []string{"ada", "ada@", "@example.com", "ada@example", "a@b@example.com", "ada@.com", "ada@example.", "a da@example.com"} {
		if err := contact.ValidateEmail(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestParsePage(t *testing.T) {
	page, size, err := contact.ParsePage("", "")
	if err != nil || page != 1 || size != 20 {
		t.Fatalf("defaults: %d %d %v", page, size, err)
	}
	page, size, err = contact.ParsePage("3", "100")
	if err != nil || page != 3 || size != 100 {
		t.Fatalf("explicit: %d %d %v", page, size, err)
	}
	for _, c := range [][2]string{{"0", ""}, {"-1", ""}, {"x", ""}, {"", "0"}, {"", "101"}, {"", "abc"}} {
		if _, _, err := contact.ParsePage(c[0], c[1]); err == nil {
			t.Errorf("page=%q size=%q: expected error", c[0], c[1])
		}
	}
	hugePage := strconv.Itoa(math.MaxInt/contact.MaxPageSize + 2)
	if _, _, err := contact.ParsePage(hugePage, "100"); err == nil {
		t.Fatalf("page=%s pageSize=100: expected overflow error", hugePage)
	}
}
