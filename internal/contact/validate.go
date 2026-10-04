package contact

import (
	"errors"
	"math"
	"math/bits"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
	maxNameLen      = 120
	maxEmailLen     = 254
)

// Contact is an external person in the organization's address book.
type Contact struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"-"`
	Name           string    `json:"name"`
	Phone          string    `json:"phone"`
	Email          *string   `json:"email"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Page is a generic paginated response envelope.
type Page[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

// ParsePage parses 1-based page and pageSize query values. Empty strings mean
// page 1 and DefaultPageSize; pageSize must be within 1..MaxPageSize.
func ParsePage(pageStr, sizeStr string) (page, size int, err error) {
	page, size = 1, DefaultPageSize
	if pageStr != "" {
		page, err = strconv.Atoi(pageStr)
		if err != nil || page < 1 {
			return 0, 0, errors.New("page must be a positive integer")
		}
	}
	if sizeStr != "" {
		size, err = strconv.Atoi(sizeStr)
		if err != nil || size < 1 || size > MaxPageSize {
			return 0, 0, errors.New("pageSize must be between 1 and 100")
		}
	}
	if _, err := listOffset(page, size); err != nil {
		return 0, 0, err
	}
	return page, size, nil
}

// listOffset is the SQL OFFSET for 1-based page and pageSize. It rejects values
// whose product would overflow int.
func listOffset(page, size int) (int, error) {
	if page < 1 || size < 1 {
		return 0, errors.New("page must be a positive integer")
	}
	skip := uint64(page - 1)
	s := uint64(size)
	hi, lo := bits.Mul64(skip, s)
	if hi != 0 || lo > uint64(math.MaxInt) {
		return 0, errors.New("page is too large")
	}
	return int(lo), nil
}

// ValidatePhone requires E.164: "+" followed by 8-15 digits, first digit not 0.
func ValidatePhone(phone string) error {
	err := errors.New("must be E.164, for example +442071838750")
	if len(phone) < 9 || len(phone) > 16 || phone[0] != '+' || phone[1] == '0' {
		return err
	}
	for i := 1; i < len(phone); i++ {
		if phone[i] < '0' || phone[i] > '9' {
			return err
		}
	}
	return nil
}

// ValidateName requires a non-blank name of at most 120 characters.
func ValidateName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" {
		return errors.New("is required")
	}
	if utf8.RuneCountInString(n) > maxNameLen {
		return errors.New("must be at most 120 characters")
	}
	return nil
}

// ValidateEmail accepts an empty string (email is optional). Otherwise it needs
// exactly one "@", a non-empty local part and a domain containing a dot that is
// neither leading nor trailing. No whitespace is allowed.
func ValidateEmail(email string) error {
	if email == "" {
		return nil
	}
	err := errors.New("must be a valid email address")
	if len(email) > maxEmailLen || strings.ContainsAny(email, " \t\r\n") || strings.Count(email, "@") != 1 {
		return err
	}
	local, domain, _ := strings.Cut(email, "@")
	if local == "" || !strings.Contains(domain, ".") ||
		strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return err
	}
	return nil
}
