package main

import "testing"

func TestParseDriverSessionID(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`{"value":{"sessionId":"abc","capabilities":{}}}`, "abc"},
		{`{"value":{"id":"def"}}`, "def"},
		{`{"sessionId":"ghi","value":{}}`, "ghi"},
		{`{"value":"jkl"}`, "jkl"},
		{`{}`, ""},
	}
	for _, tc := range cases {
		if got := parseDriverSessionID([]byte(tc.raw)); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.raw, got, tc.want)
		}
	}
}

func TestParseSessionIDs(t *testing.T) {
	raw := `{"value":[{"id":"a"},{"sessionId":"b"},"c"]}`
	got := parseSessionIDs([]byte(raw))
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("got %#v", got)
	}
}
