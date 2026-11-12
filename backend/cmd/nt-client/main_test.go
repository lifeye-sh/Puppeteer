package main

import (
	"os"
	"strings"
	"testing"
)

func TestInvalidForwardArguments(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"assets", "--once"}, "requires forward"},
		{[]string{"connect", "--asset", "fixture", "--once=false"}, "requires forward"},
		{[]string{"connect", "--asset", "fixture", "--target", "localhost:22"}, "requires forward"},
		{[]string{"assets", "--local-port", "0"}, "requires forward"},
		{[]string{"forward", "--target", "localhost:22"}, "forward requires --asset"},
		{[]string{"login"}, "login requires --remember"},
		{[]string{"assets", "--remember"}, "not valid for assets"},
		{[]string{"connect", "--allow-connect"}, "not valid for connect"},
		{[]string{"login", "--remember", "--use-login"}, "not valid for login"},
		{[]string{"logout", "--use-login"}, "not valid for logout"},
		{[]string{"login", "--remember", "--asset", "fixture"}, "not valid for login"},
	}
	for _, tail := range [][]string{{"--target", "localhost"}, {"--target", "localhost:0"}, {"--target", "localhost:65536"}, {"--target", ":22"}, {"--target", "localhost:22", "--local-port", "-1"}, {"--target", "localhost:22", "--local-port", "65536"}, {"--target", "localhost:22", "--command", "whoami"}} {
		cases = append(cases, struct {
			args []string
			want string
		}{append([]string{"forward", "--asset", "fixture"}, tail...), "forward requires --target"})
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			os.Args = append([]string{"nt-client"}, tc.args...)
			if err := run(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v; want %q", err, tc.want)
			}
		})
	}
}
