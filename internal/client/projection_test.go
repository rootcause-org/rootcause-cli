package client

import "testing"

func TestTenantSettingsDrift(t *testing.T) {
	historical := `{"source":"cli","synced_at":"2026-06-20T10:00:00Z","version":"sha256:old","settings":{"practice_name":"De Kies","newpatient_method":"waitlist"}}`

	t.Run("setting value changed", func(t *testing.T) {
		current := `{"source":"cli","synced_at":"2026-06-25T10:00:00Z","version":"sha256:new","settings":{"practice_name":"De Nieuwe Kies","newpatient_method":"waitlist"}}`
		drift, err := TenantSettingsDrift(historical, current)
		if err != nil {
			t.Fatalf("TenantSettingsDrift: %v", err)
		}
		if len(drift) != 1 || drift[0].Key != "practice_name" || drift[0].Then != "De Kies" || drift[0].Now != "De Nieuwe Kies" {
			t.Fatalf("drift = %+v, want practice_name change", drift)
		}
	})

	t.Run("version only ignored", func(t *testing.T) {
		current := `{"source":"web_form","synced_at":"2026-06-25T10:00:00Z","version":"sha256:new","settings":{"practice_name":"De Kies","newpatient_method":"waitlist"}}`
		drift, err := TenantSettingsDrift(historical, current)
		if err != nil {
			t.Fatalf("TenantSettingsDrift: %v", err)
		}
		if len(drift) != 0 {
			t.Fatalf("drift = %+v, want none", drift)
		}
	})
}
