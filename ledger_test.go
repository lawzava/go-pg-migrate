package migrate

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewLedger(t *testing.T) {
	t.Parallel()

	got, err := newLedger(`app"s`, "migrations")
	if err != nil {
		t.Fatal(err)
	}

	if want := `"app""s"."migrations"`; got.name != want {
		t.Errorf("name = %s, want %s", got.name, want)
	}

	same, _ := newLedger(`app"s`, "migrations")
	other, _ := newLedger("public", "migrations")

	if got.lockID != same.lockID || got.lockID == other.lockID {
		t.Errorf("lock IDs: %d, %d, %d; want first two equal and the third different", got.lockID, same.lockID, other.lockID)
	}

	if _, err := newLedger("pub\x00lic", "migrations"); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("NUL in schema: err = %v, want ErrInvalidConfig", err)
	}
}

type sqlStateError string

func (e sqlStateError) Error() string    { return "pg error " + string(e) }
func (e sqlStateError) SQLState() string { return string(e) }

func TestWrapLockTimeout(t *testing.T) {
	t.Parallel()

	timeout := fmt.Errorf("exec: %w", sqlStateError("55P03"))
	if err := wrapLockTimeout(timeout); !errors.Is(err, ErrLockTimeout) || !errors.Is(err, timeout) {
		t.Errorf("55P03: err = %v, want ErrLockTimeout wrapping the cause", err)
	}

	other := sqlStateError("42P07")
	if err := wrapLockTimeout(other); errors.Is(err, ErrLockTimeout) {
		t.Errorf("42P07: err = %v, want no ErrLockTimeout", err)
	}
}

func TestNewLedgerRejectsLongIdentifiers(t *testing.T) {
	t.Parallel()

	// PostgreSQL truncates identifiers to 63 bytes, so two longer names could
	// address one table while hashing to different lock keys.
	long := strings.Repeat("a", 64)

	if _, err := newLedger(long, "migrations"); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("64-byte schema: err = %v, want ErrInvalidConfig", err)
	}

	if _, err := newLedger("public", long); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("64-byte table: err = %v, want ErrInvalidConfig", err)
	}

	if _, err := newLedger("public", long[:63]); err != nil {
		t.Errorf("63-byte table: err = %v, want nil", err)
	}
}
