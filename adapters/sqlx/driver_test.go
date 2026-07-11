package sqlxadapter

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/loykin/dbstore"
	_ "modernc.org/sqlite"
)

func captureApplyPoolConfigLog(t *testing.T, cfg dbstore.PoolConfig) string {
	t.Helper()

	db, err := sqlx.Connect(DriverSQLite, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var buf bytes.Buffer
	prevOutput := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOutput)
		log.SetFlags(prevFlags)
	}()

	ApplyPoolConfig(db, cfg)
	return buf.String()
}

func TestApplyPoolConfig_WarnsWhenMaxOpenConnsBelowMaxConcurrency(t *testing.T) {
	out := captureApplyPoolConfigLog(t, dbstore.PoolConfig{MaxOpenConns: 2, MaxConcurrency: 5})
	if !strings.Contains(out, "MaxOpenConns") || !strings.Contains(out, "MaxConcurrency") {
		t.Fatalf("expected a MaxOpenConns/MaxConcurrency warning, got %q", out)
	}
}

func TestApplyPoolConfig_NoWarningWhenMaxOpenConnsAtOrAboveMaxConcurrency(t *testing.T) {
	out := captureApplyPoolConfigLog(t, dbstore.PoolConfig{MaxOpenConns: 5, MaxConcurrency: 5})
	if out != "" {
		t.Fatalf("expected no warning, got %q", out)
	}

	out = captureApplyPoolConfigLog(t, dbstore.PoolConfig{MaxOpenConns: 10, MaxConcurrency: 5})
	if out != "" {
		t.Fatalf("expected no warning, got %q", out)
	}
}

func TestApplyPoolConfig_NoWarningWhenEitherLimitUnset(t *testing.T) {
	out := captureApplyPoolConfigLog(t, dbstore.PoolConfig{MaxOpenConns: 0, MaxConcurrency: 5})
	if out != "" {
		t.Fatalf("expected no warning when MaxOpenConns is unset (unlimited), got %q", out)
	}

	out = captureApplyPoolConfigLog(t, dbstore.PoolConfig{MaxOpenConns: 2, MaxConcurrency: 0})
	if out != "" {
		t.Fatalf("expected no warning when MaxConcurrency is unset (unthrottled), got %q", out)
	}
}
