// Package jsonnum decodes the int64 fields that CometBFT and the Cosmos SDK
// REST gateway encode as JSON strings.
package jsonnum

import (
	"strconv"
	"strings"
)

// Int64 accepts "123", 123, "" and null.
type Int64 int64

func (i *Int64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*i = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*i = Int64(v)
	return nil
}
