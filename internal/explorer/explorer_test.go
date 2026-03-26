package explorer

import "testing"

func TestFilterProbes(t *testing.T) {
	all := []Probe{
		{Key: "kernel", Command: "uname"},
		{Key: "nginx_path", Command: "sh"},
	}
	out := filterProbes(all, []string{"nginx_path"})
	if len(out) != 1 || out[0].Key != "nginx_path" {
		t.Fatalf("unexpected filter result: %+v", out)
	}
}
