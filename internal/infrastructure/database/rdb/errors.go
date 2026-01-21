package rdb

import (
	"errors"

	"github.com/uptrace/bun/driver/pgdriver"
)

func IsForeignKeyViolation(err error) bool {
	var pgErr pgdriver.Error
	if errors.As(err, &pgErr) {
		return pgErr.Field('C') == "23503" // foreign_key_violation
	}
	return false
}

func IsInvalidUUIDFormat(err error) bool {
	var pgErr pgdriver.Error
	if errors.As(err, &pgErr) {
		return pgErr.Field('C') == "22P02" // invalid_text_representation (invalid UUID format)
	}
	return false
}
