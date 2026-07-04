package store

import "testing"

func TestGetRetentionDaysDefaultsWhenUnset(t *testing.T) {
	st := newTestStore(t)

	days, err := st.GetRetentionDays()
	if err != nil {
		t.Fatalf("get retention days: %v", err)
	}
	if days != DefaultRetentionDays {
		t.Fatalf("retention days = %d, want default %d", days, DefaultRetentionDays)
	}
}

func TestSetRetentionDaysRoundTrips(t *testing.T) {
	st := newTestStore(t)

	if err := st.SetRetentionDays(7); err != nil {
		t.Fatalf("set retention days: %v", err)
	}
	days, err := st.GetRetentionDays()
	if err != nil {
		t.Fatalf("get retention days: %v", err)
	}
	if days != 7 {
		t.Fatalf("retention days = %d, want 7", days)
	}
}

func TestGetRetentionDaysFallsBackOnUnparsableValue(t *testing.T) {
	st := newTestStore(t)

	if err := st.SetSetting("retention_days", "not-a-number"); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	days, err := st.GetRetentionDays()
	if err != nil {
		t.Fatalf("get retention days: %v", err)
	}
	if days != DefaultRetentionDays {
		t.Fatalf("retention days = %d, want default %d on unparsable value", days, DefaultRetentionDays)
	}
}
