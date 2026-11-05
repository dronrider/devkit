package team

import "testing"

func TestRouteRoundTrip(t *testing.T) {
	home := t.TempDir()
	want := Participant{Handle: "local", Kind: Agent, Model: "synthetic", Tier: "base", Roles: []string{RoleExec}, Route: "local", Endpoint: "http://127.0.0.1:9292/v1"}
	if err := Save(home, []Participant{want}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(home)
	if err != nil || len(got) != 1 {
		t.Fatalf("load: %v %v", got, err)
	}
	if got[0].Route != want.Route || got[0].Endpoint != want.Endpoint {
		t.Fatalf("lost route: %+v", got[0])
	}
	want.Endpoint = ""
	if err := Save(home, []Participant{want}); err == nil {
		t.Fatal("saved incomplete route")
	}
}
