package main

import (
	"strings"
	"testing"
)

func TestParseOptions(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	noEnv := env(nil)

	t.Run("password from env, default role", func(t *testing.T) {
		o, err := parseOptions([]string{"-tenant", "t1", "-email", "a@b.com"}, strings.NewReader(""),
			env(map[string]string{"PROVISION_PASSWORD": "s3cret"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.password != "s3cret" || o.role != "compliance_officer" || o.tenantID != "t1" || o.email != "a@b.com" {
			t.Errorf("unexpected options: %+v", o)
		}
	})

	t.Run("password from stdin trims the newline", func(t *testing.T) {
		o, err := parseOptions([]string{"-tenant", "t1", "-email", "a@b.com", "-role", "platform_support", "-password-stdin"},
			strings.NewReader("p4ss word\r\n"), noEnv)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.password != "p4ss word" || o.role != "platform_support" {
			t.Errorf("unexpected options: %+v", o)
		}
	})

	t.Run("errors", func(t *testing.T) {
		cases := map[string][]string{
			"missing tenant":   {"-email", "a@b.com"},
			"missing email":    {"-tenant", "t1"},
			"missing password": {"-tenant", "t1", "-email", "a@b.com"},
			"unknown flag":     {"-password", "x"},
		}
		for name, args := range cases {
			if _, err := parseOptions(args, strings.NewReader(""), noEnv); err == nil {
				t.Errorf("%s: expected error", name)
			}
		}
	})
}
