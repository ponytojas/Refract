package mode

import "testing"

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Mode
		ok   bool
	}{
		{"", SelfHost, true},
		{"selfhost", SelfHost, true},
		{"saas", SaaS, true},
		{"SaaS", "", false},
		{"hosted", "", false},
		{" saas", "", false},
	} {
		got, err := Parse(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("Parse(%q) = %q, %v; want %q, ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestChatGPT(t *testing.T) {
	if !SelfHost.ChatGPT() || SaaS.ChatGPT() {
		t.Error("the ChatGPT plan must be available only when self-hosted")
	}
}

func TestPrivateNetwork(t *testing.T) {
	if !SelfHost.PrivateNetwork() || SaaS.PrivateNetwork() {
		t.Error("private networks must be reachable only when self-hosted")
	}
}
