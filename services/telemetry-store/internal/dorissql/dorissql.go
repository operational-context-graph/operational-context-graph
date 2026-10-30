// Package dorissql provides time adapters for go-mysql-org/go-mysql against Apache Doris.
//
// Rule 1: call RegisterTimeArgs() at startup before any sql.Open call.
// Rule 2: scan DATETIME columns into DateTime, not time.Time.
package dorissql

import (
	"database/sql/driver"
	"fmt"
	"time"

	godriver "github.com/go-mysql-org/go-mysql/driver"
)

const datetimeLayout = "2006-01-02 15:04:05.000000"

// DateTime is a scan target for Doris DATETIME(6) columns.
// Doris returns DATETIME as text; scanning into time.Time fails (Rule 2).
type DateTime struct {
	Time time.Time
}

// Scan implements sql.Scanner.
func (d *DateTime) Scan(src any) error {
	switch v := src.(type) {
	case []byte:
		return d.parse(string(v))
	case string:
		return d.parse(v)
	case nil:
		d.Time = time.Time{}
		return nil
	default:
		return fmt.Errorf("dorissql: cannot scan %T into DateTime", src)
	}
}

func (d *DateTime) parse(s string) error {
	t, err := time.Parse(datetimeLayout, s)
	if err != nil {
		return fmt.Errorf("dorissql: parse datetime %q: %w", s, err)
	}
	d.Time = t.UTC()
	return nil
}

// NullDateTime is a scan target for nullable Doris DATETIME(6) columns.
type NullDateTime struct {
	Time  time.Time
	Valid bool
}

// Scan implements sql.Scanner.
func (d *NullDateTime) Scan(src any) error {
	if src == nil {
		d.Valid = false
		return nil
	}
	var dt DateTime
	if err := dt.Scan(src); err != nil {
		return err
	}
	d.Time = dt.Time
	d.Valid = true
	return nil
}

// RegisterTimeArgs registers a NamedValueChecker that converts time.Time query
// arguments to the UTC DATETIME(6) text format Doris expects (Rule 1).
// Call once at startup, before any sql.Open call.
func RegisterTimeArgs() {
	godriver.AddNamedValueChecker(func(nv *driver.NamedValue) error {
		t, ok := nv.Value.(time.Time)
		if !ok {
			return driver.ErrSkip
		}
		nv.Value = t.UTC().Format(datetimeLayout)
		return nil
	})
}
