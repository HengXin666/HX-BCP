package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestSharedInboundMigrationHonoursTheConfiguredMode pins the one condition that
// decides whether migration 37 rewrites an installation: whether that
// installation actually publishes through the shared inbound.
//
// The two ways to get it wrong are not symmetric. Missing the "no settings row"
// case leaves the reported 59 dedicated ports in place. Treating an explicit or
// legacy per_service install as shared is worse: those rows stop being compiled
// as dedicated listeners (the direct path skips any row with a family marker)
// and the aggregate family is not compiled either, so the data plane ends up
// with no listener at all.
//
// The predicate must therefore mirror internal/systemsettings exactly: a missing
// settings row means Default() (shared), while a stored row without a mode means
// migrateLegacySharedInbound (per_service).
func TestSharedInboundMigrationHonoursTheConfiguredMode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		settings  string // "" means no settings row at all
		wantOwner string
	}{
		{name: "no settings row -> shared (Default())", settings: "", wantOwner: "standard"},
		{name: "explicit shared", settings: "{\"shared_inbound\":{\"mode\":\"shared\"}}", wantOwner: "standard"},
		{name: "explicit per_service", settings: "{\"shared_inbound\":{\"mode\":\"per_service\"}}", wantOwner: ""},
		{name: "legacy row without mode -> per_service", settings: "{\"quality\":{}}", wantOwner: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "state.db")
			database, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if tc.settings != "" {
				if err := database.SetMetadata(ctx, "global_settings", tc.settings); err != nil {
					t.Fatal(err)
				}
			}
			now := "2026-01-01T00:00:00Z"
			if _, err := database.db.ExecContext(ctx, `
INSERT INTO proxy_groups(id,name,strategy,enabled,version,created_at,updated_at)
VALUES('g','g','manual',1,1,?,?)`, now, now); err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(ctx, `
INSERT INTO listeners(id,name,listener_type,bind_address,port,proxy_group_id,
  transport_json,public_endpoint_json,enabled,version,created_at,updated_at,kind,
  auth_mode,share_token,shared_inbound)
VALUES('l','l','mixed','127.0.0.1',17890,'g','{}','{}',1,1,?,?,'mixed','none','tok','')`, now, now); err != nil {
				t.Fatal(err)
			}
			// Re-run just the migration SQL, mirroring what migration 37 executes.
			if _, err := database.db.ExecContext(ctx, sharedInboundMigrationSQL()); err != nil {
				t.Fatal(err)
			}
			var owner string
			if err := database.db.QueryRowContext(ctx, "SELECT shared_inbound FROM listeners WHERE id='l'").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			if owner != tc.wantOwner {
				t.Fatalf("owner = %q, want %q", owner, tc.wantOwner)
			}
		})
	}
}

func sharedInboundMigrationSQL() string {
	for _, m := range migrations {
		if m.version == 37 {
			return m.sql
		}
	}
	return ""
}
